package harness

import (
	"context"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

func (s *commandSupervisor) waitCommand(ctx context.Context, p *ownedCommand) (winlaunch.Exit, error) {
	type observation struct {
		exit winlaunch.Exit
		err  error
	}
	finished := make(chan observation, 1)
	go func() { exit, err := p.process.Wait(ctx); finished <- observation{exit, err} }()
	var readiness <-chan time.Time
	var timer *time.Timer
	p.mu.Lock()
	initial := p.record
	p.mu.Unlock()
	ready := p.tail.ready
	if initial.ReadyText != "" {
		timer = time.NewTimer(max(time.Nanosecond, time.Until(p.started.Add(time.Duration(initial.ReadyTimeout)*time.Second))))
		defer timer.Stop()
		readiness = timer.C
	} else {
		ready = nil
	}
	for {
		select {
		case result := <-finished:
			return result.exit, result.err
		case <-ctx.Done():
			return winlaunch.Exit{}, ctx.Err()
		case <-p.expiry.C:
			p.mu.Lock()
			if time.Now().Before(p.deadline) {
				p.expiry.Reset(time.Until(p.deadline))
				p.mu.Unlock()
				continue
			}
			if p.record.State == "running" {
				p.record.State = "stopping"
				p.record.Reason = "deadline"
				p.record.Revision++
			}
			p.mu.Unlock()
			return winlaunch.Exit{}, context.DeadlineExceeded
		case <-ready:
			p.mu.Lock()
			if p.record.State == "running" {
				p.record.Ready = true
				p.record.ReadyAt = nowText()
				p.record.Revision++
				persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := s.save(persist, p.record)
				cancel()
				if err != nil {
					p.record.Reason = "readiness_persistence_failed"
					p.cancel()
				}
			}
			r := p.record
			p.mu.Unlock()
			s.event(r)
			ready, readiness = nil, nil
			if timer != nil {
				timer.Stop()
			}
		case <-readiness:
			p.tail.mu.Lock()
			matched := p.tail.readyMatched
			p.tail.mu.Unlock()
			if matched {
				readiness = nil
				continue
			}
			p.mu.Lock()
			if p.record.State == "running" {
				p.record.State = "stopping"
				p.record.Reason = "readiness_timeout"
				p.record.Revision++
			}
			p.mu.Unlock()
			return winlaunch.Exit{}, context.DeadlineExceeded
		}
	}
}
