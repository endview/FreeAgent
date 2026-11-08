package harness

import (
	"sync"
	"testing"
)

func TestRuntimeToolBindingSnapshotsConcurrentControlGateChanges(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	files, err := NewFileTools(workspace, data)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	config := DefaultConfig(workspace)
	manager := NewToolRuntimeManager(NewRuntimeTools(files), config)
	gates := [2]controlDecisionGate{}
	manager.SetControlGate(&gates[0])
	frozen := manager.Acquire()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			<-start
			for range 200 {
				if worker%2 == 0 {
					manager.SetControlGate(&gates[(worker/2)%len(gates)])
					continue
				}
				control := &ControlService{}
				service := &Service{Store: &Store{}, Config: config,
					control: control, controlMu: &sync.Mutex{}, toolManager: manager}
				service.bindCurrentToolView()
				if service.toolView == nil || service.toolView.gate == nil {
					t.Error("Run lost its control gate")
				}
				// Saved Run continuation builds from its frozen policy through
				// this same method, without binding a fresh permission profile.
				continued := manager.build(frozen.Policy())
				if continued.gate == nil || continued.policy.Generation != frozen.policy.Generation {
					t.Error("continued Run lost its gate or frozen policy")
				}
			}
		})
	}
	close(start)
	workers.Wait()
	if frozen.gate != &gates[0] {
		t.Fatal("a later binding changed an already acquired view")
	}
}
