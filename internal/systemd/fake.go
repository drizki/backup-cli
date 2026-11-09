package systemd

import (
	"fmt"
	"sync"
)

// Fake is an in-memory Manager for tests.
type Fake struct {
	mu        sync.Mutex
	Avail     bool
	Installed map[string]Unit
	Enabled   map[string]bool
	Started   []string
	Calls     []string
	// FailOn makes the named method ("Install", "Enable", ...) return an error.
	FailOn map[string]error
}

// NewFake returns an available Fake.
func NewFake() *Fake {
	return &Fake{Avail: true, Installed: map[string]Unit{}, Enabled: map[string]bool{}, FailOn: map[string]error{}}
}

func (f *Fake) call(method, name string) error {
	f.Calls = append(f.Calls, method+" "+name)
	return f.FailOn[method]
}

// Available implements Manager.
func (f *Fake) Available() bool { return f.Avail }

// Install implements Manager.
func (f *Fake) Install(u Unit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("Install", u.Name()); err != nil {
		return err
	}
	f.Installed[u.Name()] = u
	return nil
}

// Enable implements Manager.
func (f *Fake) Enable(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("Enable", name); err != nil {
		return err
	}
	if _, ok := f.Installed[name]; !ok {
		return fmt.Errorf("unit %s not installed", name)
	}
	f.Enabled[name] = true
	return nil
}

// Disable implements Manager.
func (f *Fake) Disable(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("Disable", name); err != nil {
		return err
	}
	delete(f.Enabled, name)
	return nil
}

// Remove implements Manager.
func (f *Fake) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("Remove", name); err != nil {
		return err
	}
	delete(f.Installed, name)
	delete(f.Enabled, name)
	return nil
}

// IsActive implements Manager.
func (f *Fake) IsActive(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("IsActive", name); err != nil {
		return false, err
	}
	return f.Enabled[name], nil
}

// Start implements Manager.
func (f *Fake) Start(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("Start", name); err != nil {
		return err
	}
	f.Started = append(f.Started, name)
	return nil
}
