package core

import (
	"sort"
	"strings"
	"sync"
)

// routeStore holds the current slug→Service map with a de-register debounce:
// a service missing from discovery is kept until it has been absent for
// deregisterCycles consecutive refreshes. Fed by an injectable discover func.
type routeStore struct {
	mu               sync.RWMutex
	services         map[string]Service
	missing          map[string]int // slug → consecutive missing refreshes
	duplicates       []duplicate    // latest same-project multi-port info
	deregisterCycles int
	matchSeparators  bool // treat '-' and '_' as interchangeable in path slugs
	discover         func() ([]Service, []duplicate, error)
}

// newRouteStore creates an empty store. deregisterCycles < 1 is clamped to 1.
// When matchSeparators is true, lookups treat '-' and '_' as interchangeable so
// a route registered as "module-api" is also reachable as "module_api".
func newRouteStore(discover func() ([]Service, []duplicate, error), deregisterCycles int, matchSeparators bool) *routeStore {
	if deregisterCycles < 1 {
		deregisterCycles = 1
	}
	return &routeStore{
		services:         map[string]Service{},
		missing:          map[string]int{},
		deregisterCycles: deregisterCycles,
		matchSeparators:  matchSeparators,
		discover:         discover,
	}
}

// lookup resolves a path segment to a service. Slugs are canonically
// dash-separated (see slugify), so when matchSeparators is on we retry an exact
// miss with underscores folded to dashes — letting "/module_api/" reach the
// route registered as "module-api".
func (s *routeStore) lookup(slug string) (Service, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if svc, ok := s.services[slug]; ok {
		return svc, true
	}
	if s.matchSeparators {
		if norm := strings.ReplaceAll(slug, "_", "-"); norm != slug {
			if svc, ok := s.services[norm]; ok {
				return svc, true
			}
		}
	}
	return Service{}, false
}

// forget drops key at once, without the de-register debounce: its process was
// stopped on purpose.
func (s *routeStore) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.services, key)
	delete(s.missing, key)
}

func (s *routeStore) snapshot() map[string]Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Service, len(s.services))
	for k, v := range s.services {
		out[k] = v
	}
	return out
}

// dupes returns the latest duplicate-instance info (same project, many ports).
func (s *routeStore) dupes() []duplicate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]duplicate(nil), s.duplicates...)
}

// refresh re-discovers services. Newly-seen slugs are returned in `added`
// (and registered); slugs whose served port changed (e.g. the most-recent
// instance switched) are in `repointed`; slugs missing for deregisterCycles
// consecutive refreshes are removed and returned in `removed`.
func (s *routeStore) refresh() (added, repointed []Service, removed []string, err error) {
	svcs, dups, err := s.discover()
	if err != nil {
		return nil, nil, nil, err
	}
	next := make(map[string]Service, len(svcs))
	for _, svc := range svcs {
		next[svc.Slug] = svc
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.duplicates = dups

	for slug, svc := range next {
		prev, ok := s.services[slug]
		switch {
		case !ok:
			added = append(added, svc)
		case prev.Port != svc.Port || prev.Host != svc.Host:
			repointed = append(repointed, svc)
		}
		s.services[slug] = svc // register or update
		delete(s.missing, slug)
	}
	for slug := range s.services {
		if _, present := next[slug]; present {
			continue
		}
		s.missing[slug]++
		if s.missing[slug] >= s.deregisterCycles {
			delete(s.services, slug)
			delete(s.missing, slug)
			removed = append(removed, slug)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Slug < added[j].Slug })
	sort.Slice(repointed, func(i, j int) bool { return repointed[i].Slug < repointed[j].Slug })
	sort.Strings(removed)
	return added, repointed, removed, nil
}
