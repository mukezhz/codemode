package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/mukezhz/codemode/codemode"
)

type PlanStatus string

const (
	PlanPending  PlanStatus = "pending"
	PlanApplying PlanStatus = "applying"
	PlanApplied  PlanStatus = "applied"
	PlanFailed   PlanStatus = "failed"
	PlanRejected PlanStatus = "rejected"
	PlanExpired  PlanStatus = "expired"
)

// moves is the whole state machine; anything else is refused.
var moves = map[PlanStatus][]PlanStatus{
	PlanPending:  {PlanApplying, PlanRejected, PlanExpired},
	PlanApplying: {PlanApplying, PlanApplied, PlanFailed},
}

// Plan is a set of writes held for a person, stored until they decide.
type Plan struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	// RunID is the run that held these writes; its events and errors carry the same ID.
	RunID   string          `json:"run_id"`
	Request string          `json:"request"`
	Calls   []codemode.Call `json:"calls"`
	// Applied holds one result per call that has run, in order.
	Applied   []codemode.Call `json:"applied,omitempty"`
	Status    PlanStatus      `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	History   []PlanChange    `json:"history"`
	// Version is the store's compare-and-set counter; leave it alone.
	Version int `json:"version"`
}

type PlanChange struct {
	Status PlanStatus `json:"status"`
	At     time.Time  `json:"at"`
	By     string     `json:"by,omitempty"`
	Note   string     `json:"note,omitempty"`
}

// Decided is the change that ended the plan's wait, if any.
func (plan Plan) Decided() (PlanChange, bool) {
	for _, change := range plan.History {
		if change.Status != PlanPending {
			return change, true
		}
	}
	return PlanChange{}, false
}

func (plan *Plan) move(to PlanStatus, at time.Time, by, note string) error {
	if !slices.Contains(moves[plan.Status], to) {
		return plan.decidedError()
	}
	plan.Status = to
	plan.History = append(plan.History, PlanChange{Status: to, At: at, By: by, Note: note})
	return nil
}

func (plan Plan) decidedError() error {
	change, _ := plan.Decided()
	detail := fmt.Sprintf("at %s", change.At.UTC().Format(time.RFC3339))
	if change.By != "" {
		detail = "by " + change.By + " " + detail
	}
	if plan.Status == PlanExpired {
		return fmt.Errorf("plan %s expired at %s: %w", plan.ID, plan.ExpiresAt.UTC().Format(time.RFC3339), ErrPlanExpired)
	}
	return fmt.Errorf("plan %s is already %s (%s): %w", plan.ID, plan.Status, detail, ErrPlanDecided)
}

// Pending is the error a run returns when a write is held. It is the stored
// plan, so ID and ExpiresAt are what the client needs. Without Options.Plans
// the plan is not stored and ID is empty: show Calls, but nothing can apply them.
type Pending struct {
	Plan
}

func (pending *Pending) Error() string {
	if pending.ID == "" {
		return fmt.Sprintf("%d write(s) wait for approval", len(pending.Calls))
	}
	return fmt.Sprintf("plan %s: %d write(s) wait for approval until %s",
		pending.ID, len(pending.Calls), pending.ExpiresAt.UTC().Format(time.RFC3339))
}

var (
	ErrNoPlan      = errors.New("no such plan")
	ErrPlanExpired = errors.New("plan expired")
	ErrPlanDecided = errors.New("plan already decided")
	// ErrPlanChanged is what a PlanStore returns when the stored version moved on.
	ErrPlanChanged = errors.New("plan changed since it was read")
)

// PlanStore keeps plans. Update writes plan only if the stored version equals
// plan.Version, then increments both; otherwise it returns ErrPlanChanged. Get
// and List return ErrNoPlan, never another owner's plan. List is newest first;
// an empty status lists every status.
type PlanStore interface {
	Create(ctx context.Context, plan Plan) error
	Get(ctx context.Context, owner, id string) (Plan, error)
	List(ctx context.Context, owner string, status PlanStatus) ([]Plan, error)
	Update(ctx context.Context, plan *Plan) error
}

const DefaultPlanTTL = 24 * time.Hour

type PlanOptions struct {
	// How long a plan waits for a decision; zero takes DefaultPlanTTL.
	TTL time.Duration
	// Receives plan.held, plan.applying, plan.applied, plan.failed, plan.rejected and plan.expired. It must not block.
	OnEvent func(Event)
	// Now is for tests; nil uses time.Now.
	Now func() time.Time
}

// Plans is the approval API: runs hold writes in it, and people decide on them
// through it. Safe for concurrent use; the store serialises decisions.
type Plans struct {
	store   PlanStore
	options PlanOptions
}

func NewPlans(store PlanStore, options PlanOptions) *Plans {
	if options.TTL <= 0 {
		options.TTL = DefaultPlanTTL
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Plans{store: store, options: options}
}

func (plans *Plans) hold(ctx context.Context, owner, runID, request string, calls []codemode.Call) (Plan, error) {
	now := plans.options.Now()
	plan := Plan{
		ID: newID(16), Owner: owner, RunID: runID, Request: request, Calls: calls,
		Status: PlanPending, CreatedAt: now, ExpiresAt: now.Add(plans.options.TTL),
		History: []PlanChange{{Status: PlanPending, At: now, By: owner}},
	}
	if err := plans.store.Create(ctx, plan); err != nil {
		return Plan{}, fmt.Errorf("save plan: %w", err)
	}
	plans.emit(Event{Kind: EventPlanHeld, RunID: runID, PlanID: plan.ID, Count: len(calls)})
	return plan, nil
}

// Get returns a plan, moving it to expired first if its time has passed.
func (plans *Plans) Get(ctx context.Context, owner, id string) (Plan, error) {
	plan, err := plans.store.Get(ctx, owner, id)
	if err != nil {
		return Plan{}, planError(id, err)
	}
	return plans.expire(ctx, plan)
}

// List returns owner's plans in status, newest first; an empty status lists all.
func (plans *Plans) List(ctx context.Context, owner string, status PlanStatus) ([]Plan, error) {
	listed, err := plans.store.List(ctx, owner, "")
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	kept := make([]Plan, 0, len(listed))
	for _, plan := range listed {
		if plan, err = plans.expire(ctx, plan); err != nil {
			return nil, err
		}
		if status == "" || plan.Status == status {
			kept = append(kept, plan)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].CreatedAt.After(kept[j].CreatedAt) })
	return kept, nil
}

// ponytail: expiry happens when a plan is read, so an unread plan stays pending in storage past its time.
func (plans *Plans) expire(ctx context.Context, plan Plan) (Plan, error) {
	now := plans.options.Now()
	if plan.Status != PlanPending || now.Before(plan.ExpiresAt) {
		return plan, nil
	}
	if err := plan.move(PlanExpired, now, "", "not decided in time"); err != nil {
		return plan, err
	}
	if err := plans.store.Update(ctx, &plan); err != nil {
		if errors.Is(err, ErrPlanChanged) {
			return plans.store.Get(ctx, plan.Owner, plan.ID)
		}
		return plan, fmt.Errorf("plan %s: expire: %w", plan.ID, err)
	}
	plans.emit(Event{Kind: EventPlanExpired, RunID: plan.RunID, PlanID: plan.ID})
	return plan, nil
}

// Reject discards a pending plan; none of its calls run.
func (plans *Plans) Reject(ctx context.Context, owner, id, note string) (Plan, error) {
	plan, err := plans.decide(ctx, owner, id, PlanRejected, note)
	if err != nil {
		return plan, err
	}
	plans.emit(Event{Kind: EventPlanRejected, RunID: plan.RunID, PlanID: plan.ID})
	return plan, nil
}

// Approve runs a pending plan once. Pass the caller's current runtime: each call
// passes validation, the registry and your handler again. On a failed call the
// plan is failed and the error names the call; calls after it do not run.
func (plans *Plans) Approve(ctx context.Context, mode codemode.CodeMode, owner, id string) (Plan, error) {
	plan, err := plans.decide(ctx, owner, id, PlanApplying, "approved")
	if err != nil {
		return plan, err
	}
	return plans.apply(ctx, mode, plan)
}

// Resume continues a plan left applying by a crash, from the first call with no
// recorded result. That call may have committed before the crash, so its
// handler should be idempotent, or check before resuming.
func (plans *Plans) Resume(ctx context.Context, mode codemode.CodeMode, owner, id string) (Plan, error) {
	plan, err := plans.Get(ctx, owner, id)
	if err != nil {
		return plan, err
	}
	if plan.Status != PlanApplying {
		return plan, fmt.Errorf("plan %s is %s, not applying; only an interrupted plan resumes: %w", plan.ID, plan.Status, ErrPlanDecided)
	}
	if err := plan.move(PlanApplying, plans.options.Now(), owner, fmt.Sprintf("resumed at call %d of %d", len(plan.Applied)+1, len(plan.Calls))); err != nil {
		return plan, err
	}
	if err := plans.save(ctx, &plan); err != nil {
		return plan, err
	}
	return plans.apply(ctx, mode, plan)
}

func (plans *Plans) decide(ctx context.Context, owner, id string, to PlanStatus, note string) (Plan, error) {
	plan, err := plans.Get(ctx, owner, id)
	if err != nil {
		return plan, err
	}
	// Only a waiting plan is decided; an applying one goes through Resume, never a second Approve.
	if plan.Status != PlanPending {
		return plan, plan.decidedError()
	}
	if err := plan.move(to, plans.options.Now(), owner, note); err != nil {
		return plan, err
	}
	return plan, plans.save(ctx, &plan)
}

// save turns a lost compare-and-set into the error that explains it.
func (plans *Plans) save(ctx context.Context, plan *Plan) error {
	err := plans.store.Update(ctx, plan)
	if errors.Is(err, ErrPlanChanged) {
		if current, getErr := plans.store.Get(ctx, plan.Owner, plan.ID); getErr == nil {
			return current.decidedError()
		}
	}
	if err != nil {
		return fmt.Errorf("plan %s: save: %w", plan.ID, err)
	}
	return nil
}

func (plans *Plans) apply(ctx context.Context, mode codemode.CodeMode, plan Plan) (Plan, error) {
	plans.emit(Event{Kind: EventPlanApplying, RunID: plan.RunID, PlanID: plan.ID, Count: len(plan.Calls) - len(plan.Applied)})
	approved := codemode.WithApproval(ctx)
	for index := len(plan.Applied); index < len(plan.Calls); index++ {
		call := plan.Calls[index]
		started := time.Now()
		output, err := mode.Execute(approved, call.Name, call.Input)
		result := codemode.Call{Name: call.Name, Input: call.Input, Output: output, Duration: time.Since(started)}
		if err != nil {
			result.Error = err.Error()
		}
		plan.Applied = append(plan.Applied, result)
		plans.emit(Event{Kind: EventCallDone, RunID: plan.RunID, PlanID: plan.ID, Name: call.Name, Input: call.Input, Duration: result.Duration, Err: result.Error})
		if err != nil {
			failure := fmt.Errorf("plan %s: call %d of %d (%s): %w", plan.ID, index+1, len(plan.Calls), call.Name, err)
			_ = plan.move(PlanFailed, plans.options.Now(), "", failure.Error())
			if saveErr := plans.save(ctx, &plan); saveErr != nil {
				return plan, errors.Join(failure, saveErr)
			}
			plans.emit(Event{Kind: EventPlanFailed, RunID: plan.RunID, PlanID: plan.ID, Name: call.Name, Err: err.Error()})
			return plan, failure
		}
		// Recorded before the next call, so a crash resumes after this one.
		if err := plans.save(ctx, &plan); err != nil {
			return plan, err
		}
	}
	if err := plan.move(PlanApplied, plans.options.Now(), "", ""); err != nil {
		return plan, err
	}
	if err := plans.save(ctx, &plan); err != nil {
		return plan, err
	}
	plans.emit(Event{Kind: EventPlanApplied, RunID: plan.RunID, PlanID: plan.ID, Count: len(plan.Applied)})
	return plan, nil
}

func (plans *Plans) emit(event Event) {
	if plans.options.OnEvent != nil {
		event.Time = plans.options.Now()
		plans.options.OnEvent(event)
	}
}

func planError(id string, err error) error {
	if errors.Is(err, ErrNoPlan) {
		return fmt.Errorf("plan %s: %w", id, ErrNoPlan)
	}
	return fmt.Errorf("plan %s: %w", id, err)
}

type memoryPlanStore struct {
	mutex sync.Mutex
	plans map[string][]byte
}

var _ PlanStore = (*memoryPlanStore)(nil)

// NewMemoryPlanStore keeps plans in this process.
// ponytail: lost on restart and invisible to other replicas; use agent/sqlplans for either.
func NewMemoryPlanStore() PlanStore {
	return &memoryPlanStore{plans: map[string][]byte{}}
}

// Plans are stored encoded, so a caller holding a Plan cannot change the stored one.
func (store *memoryPlanStore) Create(_ context.Context, plan Plan) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if _, exists := store.plans[plan.ID]; exists {
		return fmt.Errorf("plan %s already exists", plan.ID)
	}
	encoded, err := json.Marshal(plan)
	store.plans[plan.ID] = encoded
	return err
}

func (store *memoryPlanStore) Get(_ context.Context, owner, id string) (Plan, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.get(owner, id)
}

func (store *memoryPlanStore) get(owner, id string) (Plan, error) {
	var plan Plan
	encoded, found := store.plans[id]
	if !found {
		return Plan{}, ErrNoPlan
	}
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return Plan{}, err
	}
	if plan.Owner != owner {
		return Plan{}, ErrNoPlan
	}
	return plan, nil
}

func (store *memoryPlanStore) List(_ context.Context, owner string, status PlanStatus) ([]Plan, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var listed []Plan
	for id := range store.plans {
		plan, err := store.get(owner, id)
		if errors.Is(err, ErrNoPlan) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if status == "" || plan.Status == status {
			listed = append(listed, plan)
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].CreatedAt.After(listed[j].CreatedAt) })
	return listed, nil
}

func (store *memoryPlanStore) Update(_ context.Context, plan *Plan) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	stored, err := store.get(plan.Owner, plan.ID)
	if err != nil {
		return err
	}
	if stored.Version != plan.Version {
		return ErrPlanChanged
	}
	plan.Version++
	encoded, err := json.Marshal(plan)
	if err != nil {
		plan.Version--
		return err
	}
	store.plans[plan.ID] = encoded
	return nil
}

func newID(bytes int) string {
	random := make([]byte, bytes)
	_, _ = rand.Read(random)
	return hex.EncodeToString(random)
}
