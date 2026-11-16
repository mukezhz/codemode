package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mukezhz/codemode/codemode"
)

// Pending is the error a run returns when a write is held for approval. Show
// Calls to the person; on approval, apply them with ApplyPlan(ID), or with
// Apply(Calls) when no PlanStore is configured.
type Pending struct {
	// Empty when Options.Plans is nil.
	ID    string
	Calls []codemode.Call
}

func (pending *Pending) Error() string {
	return fmt.Sprintf("%d write(s) wait for approval", len(pending.Calls))
}

// ErrNoPlan is returned for a plan that does not exist, has expired, was
// already applied, or belongs to another owner.
var ErrNoPlan = errors.New("no such plan")

// PlanStore keeps held writes on the server, so the client sends back only an
// ID and cannot change what was approved. Take removes the plan: it applies once.
type PlanStore interface {
	Save(ctx context.Context, owner string, calls []codemode.Call) (id string, err error)
	Take(ctx context.Context, owner, id string) ([]codemode.Call, error)
}

// DefaultPlanTTL is how long a memory plan waits for approval.
const DefaultPlanTTL = 15 * time.Minute

type memoryPlans struct {
	ttl   time.Duration
	mutex sync.Mutex
	plans map[string]memoryPlan
}

type memoryPlan struct {
	owner   string
	calls   []codemode.Call
	expires time.Time
}

var _ PlanStore = (*memoryPlans)(nil)

// NewMemoryPlans keeps plans in this process for ttl (DefaultPlanTTL when zero).
// ponytail: one process only; back PlanStore with your database when there are replicas.
func NewMemoryPlans(ttl time.Duration) PlanStore {
	if ttl <= 0 {
		ttl = DefaultPlanTTL
	}
	return &memoryPlans{ttl: ttl, plans: map[string]memoryPlan{}}
}

func (store *memoryPlans) Save(_ context.Context, owner string, calls []codemode.Call) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	id := hex.EncodeToString(random)
	now := time.Now()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for key, plan := range store.plans {
		if now.After(plan.expires) {
			delete(store.plans, key)
		}
	}
	store.plans[id] = memoryPlan{owner: owner, calls: append([]codemode.Call(nil), calls...), expires: now.Add(store.ttl)}
	return id, nil
}

func (store *memoryPlans) Take(_ context.Context, owner, id string) ([]codemode.Call, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	plan, found := store.plans[id]
	if !found || plan.owner != owner {
		return nil, ErrNoPlan
	}
	delete(store.plans, id)
	if time.Now().After(plan.expires) {
		return nil, ErrNoPlan
	}
	return plan.calls, nil
}

// ApplyPlan runs an approved plan once. Pass the caller's current runtime: every
// call passes validation, the registry and your transport again.
func ApplyPlan(ctx context.Context, mode codemode.CodeMode, store PlanStore, owner, id string) ([]codemode.Call, error) {
	calls, err := store.Take(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	return Apply(ctx, mode, calls)
}
