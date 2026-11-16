package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
)

// fairness keeps one user from starving the rest: a request rate and a cap on programs running at once.
// ponytail: in-memory and per process; a shared store (Redis) once there is more than one replica.
type fairness struct {
	perMinute  int
	concurrent int

	mutex sync.Mutex
	users map[string]*allowance
}

type allowance struct {
	limiter *rate.Limiter
	running chan struct{}
}

func (fairness *fairness) of(user string) *allowance {
	fairness.mutex.Lock()
	defer fairness.mutex.Unlock()
	if fairness.users == nil {
		fairness.users = map[string]*allowance{}
	}
	if found, ok := fairness.users[user]; ok {
		return found
	}
	created := &allowance{
		limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(fairness.perMinute)), fairness.perMinute),
		running: make(chan struct{}, fairness.concurrent),
	}
	fairness.users[user] = created
	return created
}

func (fairness *fairness) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		identity, err := auth.From(request.Context())
		if err != nil {
			fail(writer, http.StatusUnauthorized, err)
			return
		}
		allowance := fairness.of(identity.User)
		if reservation := allowance.limiter.Reserve(); reservation.Delay() > 0 {
			reservation.Cancel()
			writer.Header().Set("Retry-After", strconv.Itoa(int(time.Minute.Seconds())/fairness.perMinute+1))
			fail(writer, http.StatusTooManyRequests, errTooMany("requests per minute"))
			return
		}
		select {
		case allowance.running <- struct{}{}:
			defer func() { <-allowance.running }()
		default:
			fail(writer, http.StatusTooManyRequests, errTooMany("requests at once"))
			return
		}
		next(writer, request)
	}
}

type errTooMany string

func (err errTooMany) Error() string { return "too many " + string(err) }

// auditor writes one JSON line per capability call. Inputs are hashed: the log
// proves what was called without becoming a second copy of the data.
type auditor struct {
	mutex  sync.Mutex
	writer io.Writer
}

type auditRecord struct {
	Time       time.Time `json:"time"`
	User       string    `json:"user"`
	Role       string    `json:"role"`
	Capability string    `json:"capability"`
	InputHash  string    `json:"input_sha256"`
	Held       bool      `json:"held,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

// record takes the call events of one caller's runs and approved plans.
func (auditor *auditor) record(identity auth.Identity, event agent.Event) {
	if auditor == nil || (event.Kind != agent.EventCallDone && event.Kind != agent.EventCallHeld) {
		return
	}
	sum := sha256.Sum256(event.Input)
	line, _ := json.Marshal(auditRecord{
		Time: event.Time.UTC(), User: identity.User, Role: string(identity.Role), Capability: event.Name,
		InputHash: hex.EncodeToString(sum[:]), Held: event.Kind == agent.EventCallHeld, Error: event.Err,
		DurationMS: event.Duration.Milliseconds(),
	})
	auditor.mutex.Lock()
	auditor.writer.Write(append(line, '\n'))
	auditor.mutex.Unlock()
}
