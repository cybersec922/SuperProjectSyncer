package watcher

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceInterval = 500 * time.Millisecond

// OnFolderChange is called with top-level folder relative to root when changes settle.
type OnFolderChange func(folder string)

// Watcher debounces fs events into folder-level batches.
type Watcher struct {
	root     string
	onChange OnFolderChange
	fw       *fsnotify.Watcher
	mu       sync.Mutex
	pending  map[string]*time.Timer
	closed   bool
}

func New(root string, onChange OnFolderChange) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:     filepath.Clean(root),
		onChange: onChange,
		fw:       fw,
		pending:  make(map[string]*time.Timer),
	}
	if err := w.addRecursive(w.root); err != nil {
		fw.Close()
		return nil, err
	}
	return w, nil
}

func (w *Watcher) addRecursive(dir string) error {
	if err := w.fw.Add(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := w.addRecursive(filepath.Join(dir, e.Name())); err != nil {
				log.Printf("watcher: skip %s: %v", e.Name(), err)
			}
		}
	}
	return nil
}

func (w *Watcher) Run() {
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	if ev.Op&fsnotify.Chmod == fsnotify.Chmod {
		return
	}
	path := filepath.Clean(ev.Name)
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	folder := topLevelFolder(rel)
	if ev.Op&fsnotify.Create == fsnotify.Create {
		if statIsDir(path) {
			_ = w.addRecursive(path)
		}
	}
	w.schedule(folder)
}

func topLevelFolder(rel string) string {
	parts := strings.SplitN(rel, "/", 2)
	if len(parts) == 1 {
		return "."
	}
	return parts[0]
}

func (w *Watcher) schedule(folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if t, ok := w.pending[folder]; ok {
		t.Stop()
	}
	f := folder
	w.pending[folder] = time.AfterFunc(debounceInterval, func() {
		w.mu.Lock()
		delete(w.pending, f)
		w.mu.Unlock()
		if w.onChange != nil {
			w.onChange(f)
		}
	})
}

func (w *Watcher) Close() error {
	w.mu.Lock()
	w.closed = true
	for _, t := range w.pending {
		t.Stop()
	}
	w.mu.Unlock()
	return w.fw.Close()
}

func statIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
