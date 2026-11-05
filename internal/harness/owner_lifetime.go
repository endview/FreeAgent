package harness

import "context"

func (tasks *TaskService) OwnerDone() <-chan struct{} {
	if tasks != nil && tasks.core != nil && tasks.core.Store != nil && tasks.core.Store.pgOwner != nil {
		return tasks.core.Store.pgOwner.ctx.Done()
	}
	return nil
}
func (tasks *TaskService) OwnerError() error {
	if tasks != nil && tasks.core != nil && tasks.core.Store != nil {
		return tasks.core.Store.db.ready()
	}
	return nil
}

// KeepOwnerAlive inspects existing facts; it does not create a lifecycle store.
func (tasks *TaskService) KeepOwnerAlive(ctx context.Context) (bool, error) {
	var count int
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.runs WHERE status IN ('running','waiting_confirmation')`).Scan(&count)
	return count > 0, err
}
