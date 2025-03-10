package wrr

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"net/http"
	"strconv"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/traefik/traefik/v3/pkg/config/dynamic"
)

type namedHandler struct {
	http.Handler
	name       string
	hashedName string
	weight     float64
	deadline   float64
}

type stickyCookie struct {
	name     string
	secure   bool
	httpOnly bool
	sameSite string
	maxAge   int
	path     string
	domain   string
}

func convertSameSite(sameSite string) http.SameSite {
	switch sameSite {
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteDefaultMode
	}
}

// Balancer is a WeightedRoundRobin load balancer based on Earliest Deadline First (EDF).
// (https://en.wikipedia.org/wiki/Earliest_deadline_first_scheduling)
// Each pick from the schedule has the earliest deadline entry selected.
// Entries have deadlines set at currentDeadline + 1 / weight,
// providing weighted round-robin behavior with floating point weights and an O(log n) pick time.
type Balancer struct {
	stickyCookie     *stickyCookie
	wantsHealthCheck bool

	handlersMu sync.RWMutex
	// References all the handlers by name and also by the hashed value of the name.
	stickyMap              map[string]*namedHandler
	compatibilityStickyMap map[string]*namedHandler
	handlers               []*namedHandler
	curDeadline            float64
	// status is a record of which child services of the Balancer are healthy, keyed
	// by name of child service. A service is initially added to the map when it is
	// created via Add, and it is later removed or added to the map as needed,
	// through the SetStatus method.
	status map[string]struct{}
	// updaters is the list of hooks that are run (to update the Balancer
	// parent(s)), whenever the Balancer status changes.
	updaters []func(bool)
	// fenced is the list of terminating yet still serving child services.
	fenced map[string]struct{}
}

// New creates a new load balancer.
func New(sticky *dynamic.Sticky, wantHealthCheck bool) *Balancer {
	balancer := &Balancer{
		status:           make(map[string]struct{}),
		fenced:           make(map[string]struct{}),
		wantsHealthCheck: wantHealthCheck,
	}
	if sticky != nil && sticky.Cookie != nil {
		balancer.stickyCookie = &stickyCookie{
			name:     sticky.Cookie.Name,
			secure:   sticky.Cookie.Secure,
			httpOnly: sticky.Cookie.HTTPOnly,
			sameSite: sticky.Cookie.SameSite,
			maxAge:   sticky.Cookie.MaxAge,
			path:     "/",
			domain:   sticky.Cookie.Domain,
		}
		if sticky.Cookie.Path != nil {
			balancer.stickyCookie.path = *sticky.Cookie.Path
		}

		balancer.stickyMap = make(map[string]*namedHandler)
		balancer.compatibilityStickyMap = make(map[string]*namedHandler)
	}

	return balancer
}

// Len implements heap.Interface/sort.Interface.
func (b *Balancer) Len() int { return len(b.handlers) }

// Less implements heap.Interface/sort.Interface.
func (b *Balancer) Less(i, j int) bool {
	return b.handlers[i].deadline < b.handlers[j].deadline
}

// Swap implements heap.Interface/sort.Interface.
func (b *Balancer) Swap(i, j int) {
	b.handlers[i], b.handlers[j] = b.handlers[j], b.handlers[i]
}

// Push implements heap.Interface for pushing an item into the heap.
func (b *Balancer) Push(x interface{}) {
	h, ok := x.(*namedHandler)
	if !ok {
		return
	}

	b.handlers = append(b.handlers, h)
}

// Pop implements heap.Interface for popping an item from the heap.
// It panics if b.Len() < 1.
func (b *Balancer) Pop() interface{} {
	h := b.handlers[len(b.handlers)-1]
	b.handlers = b.handlers[0 : len(b.handlers)-1]
	return h
}

// SetStatus sets on the balancer that its given child is now of the given
// status. balancerName is only needed for logging purposes.
func (b *Balancer) SetStatus(ctx context.Context, childName string, up bool) {
	b.handlersMu.Lock()
	defer b.handlersMu.Unlock()

	upBefore := len(b.status) > 0

	status := "DOWN"
	if up {
		status = "UP"
	}

	log.Ctx(ctx).Debug().Msgf("Setting status of %s to %v", childName, status)

	if up {
		b.status[childName] = struct{}{}
	} else {
		delete(b.status, childName)
	}

	upAfter := len(b.status) > 0
	status = "DOWN"
	if upAfter {
		status = "UP"
	}

	// No Status Change
	if upBefore == upAfter {
		// We're still with the same status, no need to propagate
		log.Ctx(ctx).Debug().Msgf("Still %s, no need to propagate", status)
		return
	}

	// Status Change
	log.Ctx(ctx).Debug().Msgf("Propagating new %s status", status)
	for _, fn := range b.updaters {
		fn(upAfter)
	}
}

// RegisterStatusUpdater adds fn to the list of hooks that are run when the
// status of the Balancer changes.
// Not thread safe.
func (b *Balancer) RegisterStatusUpdater(fn func(up bool)) error {
	if !b.wantsHealthCheck {
		return errors.New("healthCheck not enabled in config for this weighted service")
	}
	b.updaters = append(b.updaters, fn)
	return nil
}

var errNoAvailableServer = errors.New("no available server")

func (b *Balancer) nextServer() (*namedHandler, error) {
	b.handlersMu.Lock()
	defer b.handlersMu.Unlock()

	if len(b.handlers) == 0 || len(b.status) == 0 || len(b.fenced) == len(b.handlers) {
		return nil, errNoAvailableServer
	}

	var handler *namedHandler
	for {
		// Pick handler with closest deadline.
		handler = heap.Pop(b).(*namedHandler)

		// curDeadline should be handler's deadline so that new added entry would have a fair competition environment with the old ones.
		b.curDeadline = handler.deadline
		handler.deadline += 1 / handler.weight

		heap.Push(b, handler)
		if _, ok := b.status[handler.name]; ok {
			if _, ok := b.fenced[handler.name]; !ok {
				// do not select a fenced handler.
				break
			}
		}
	}

	log.Debug().Msgf("Service selected by WRR: %s", handler.name)
	return handler, nil
}

func (b *Balancer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if b.stickyCookie != nil {
		cookie, err := req.Cookie(b.stickyCookie.name)

		if err != nil && !errors.Is(err, http.ErrNoCookie) {
			log.Warn().Err(err).Msg("Error while reading cookie")
		}

		if err == nil && cookie != nil {
			b.handlersMu.RLock()
			handler, ok := b.stickyMap[cookie.Value]
			b.handlersMu.RUnlock()

			if ok && handler != nil {
				b.handlersMu.RLock()
				_, isHealthy := b.status[handler.name]
				b.handlersMu.RUnlock()
				if isHealthy {
					handler.ServeHTTP(w, req)
					return
				}
			}

			b.handlersMu.RLock()
			handler, ok = b.compatibilityStickyMap[cookie.Value]
			b.handlersMu.RUnlock()

			if ok && handler != nil {
				b.handlersMu.RLock()
				_, isHealthy := b.status[handler.name]
				b.handlersMu.RUnlock()
				if isHealthy {
					b.writeStickyCookie(w, handler)

					handler.ServeHTTP(w, req)
					return
				}
			}
		}
	}

	server, err := b.nextServer()
	if err != nil {
		if errors.Is(err, errNoAvailableServer) {
			http.Error(w, errNoAvailableServer.Error(), http.StatusServiceUnavailable)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if b.stickyCookie != nil {
		b.writeStickyCookie(w, server)
	}

	server.ServeHTTP(w, req)
}

func (b *Balancer) writeStickyCookie(w http.ResponseWriter, handler *namedHandler) {
	cookie := &http.Cookie{
		Name:     b.stickyCookie.name,
		Value:    handler.hashedName,
		Path:     b.stickyCookie.path,
		HttpOnly: b.stickyCookie.httpOnly,
		Secure:   b.stickyCookie.secure,
		SameSite: convertSameSite(b.stickyCookie.sameSite),
		MaxAge:   b.stickyCookie.maxAge,
		Domain:   b.stickyCookie.domain,
	}
	http.SetCookie(w, cookie)
}

// Add adds a handler.
// A handler with a non-positive weight is ignored.
func (b *Balancer) Add(name string, handler http.Handler, weight *int, fenced bool) {
	w := 1
	if weight != nil {
		w = *weight
	}

	if w <= 0 { // non-positive weight is meaningless
		return
	}

	h := &namedHandler{Handler: handler, name: name, weight: float64(w)}

	b.handlersMu.Lock()
	h.deadline = b.curDeadline + 1/h.weight
	heap.Push(b, h)
	b.status[name] = struct{}{}
	if fenced {
		b.fenced[name] = struct{}{}
	}

	if b.stickyCookie != nil {
		sha256HashedName := sha256Hash(name)
		h.hashedName = sha256HashedName

		b.stickyMap[sha256HashedName] = h
		b.compatibilityStickyMap[name] = h

		hashedName := fnvHash(name)
		b.compatibilityStickyMap[hashedName] = h

		// server.URL was fnv hashed in service.Manager
		// so we can have "double" fnv hash in already existing cookies
		hashedName = fnvHash(hashedName)
		b.compatibilityStickyMap[hashedName] = h
	}
	b.handlersMu.Unlock()
}

func fnvHash(input string) string {
	hasher := fnv.New64()
	// We purposely ignore the error because the implementation always returns nil.
	_, _ = hasher.Write([]byte(input))

	return strconv.FormatUint(hasher.Sum64(), 16)
}

func sha256Hash(input string) string {
	hash := sha256.New()
	// We purposely ignore the error because the implementation always returns nil.
	_, _ = hash.Write([]byte(input))

	hashedInput := hex.EncodeToString(hash.Sum(nil))
	if len(hashedInput) < 16 {
		return hashedInput
	}
	return hashedInput[:16]
}
