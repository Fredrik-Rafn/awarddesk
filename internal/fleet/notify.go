package fleet

func (f *Fleet) notifyLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *Fleet) ProxyListChanged() {
	f.pool.Invalidate()
	f.mu.Lock()
	f.notifyLocked()
	f.mu.Unlock()
}
