package mcpstdio

import "os/exec"

// ManagedTree reuses the same process-group/Job boundary for local commands.
// Cleanup is best effort; it does not roll back effects or provide a sandbox.
type ManagedTree struct{ tree managedProcessTree }

func ConfigureCommand(cmd *exec.Cmd) { configureManagedCommand(cmd) }

func AttachCommand(cmd *exec.Cmd) (*ManagedTree, error) {
	tree, err := attachManagedProcessTree(cmd)
	if err != nil {
		return nil, err
	}
	return &ManagedTree{tree: tree}, nil
}

func (tree *ManagedTree) Terminate()   { tree.tree.terminate() }
func (tree *ManagedTree) Kill() error  { return tree.tree.kill() }
func (tree *ManagedTree) Close() error { return tree.tree.close() }
