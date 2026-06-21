package approval

import (
	"sync"

	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/state"
)

// Queue manages folder-level approval for ask_folder mode.
type Queue struct {
	mode      config.Approval
	store     *state.Store
	syncName  string
	mu        sync.Mutex
	approved  map[string]struct{}
	waiters   map[string][]chan struct{}
	onPending func(folder string)
}

func New(mode config.Approval, store *state.Store, syncName string, onPending func(string)) *Queue {
	return &Queue{
		mode:      mode,
		store:     store,
		syncName:  syncName,
		approved:  make(map[string]struct{}),
		waiters:   make(map[string][]chan struct{}),
		onPending: onPending,
	}
}

func (q *Queue) Mode() config.Approval {
	return q.mode
}

// Wait blocks until folder is approved or auto mode skips wait.
func (q *Queue) Wait(folder string) bool {
	if q.mode == config.ApprovalAuto {
		return true
	}
	q.mu.Lock()
	if _, ok := q.approved[folder]; ok {
		q.mu.Unlock()
		return true
	}
	ch := make(chan struct{})
	q.waiters[folder] = append(q.waiters[folder], ch)
	q.mu.Unlock()

	if q.store != nil {
		_, _ = q.store.AddPending(q.syncName, folder)
	}
	if q.onPending != nil {
		q.onPending(folder)
	}

	<-ch
	return true
}

// Approve releases waiters for folder.
func (q *Queue) Approve(folder string) error {
	q.mu.Lock()
	q.approved[folder] = struct{}{}
	waiters := q.waiters[folder]
	delete(q.waiters, folder)
	q.mu.Unlock()

	for _, ch := range waiters {
		close(ch)
	}
	if q.store != nil {
		return q.store.RemovePending(q.syncName, folder)
	}
	return nil
}

func (q *Queue) Pending() ([]string, error) {
	if q.store == nil {
		return nil, nil
	}
	return q.store.ListPending(q.syncName)
}
