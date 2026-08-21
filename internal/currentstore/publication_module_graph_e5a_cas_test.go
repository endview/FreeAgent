package currentstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestW2E5APublicationCASCompetitionHasOneWinner(t *testing.T) {
	fixture := newE5APublicationFixture(
		t,
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		nil,
		nil,
		nil,
	)
	first, _, _ := fixture.freeze(t, "cas-first")
	second, _, _ := fixture.freeze(t, "cas-second")
	inputs := []PublishControlCatalogInput{first, second}

	start := make(chan struct{})
	errorsByIndex := make([]error, len(inputs))
	var group sync.WaitGroup
	for index := range inputs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, errorsByIndex[index] = fixture.publication.store.PublishControlCatalog(
				context.Background(),
				inputs[index],
			)
		}(index)
	}
	close(start)
	group.Wait()

	successes := 0
	conflicts := 0
	for _, err := range errorsByIndex {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrPublicationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected E5-A CAS outcome: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"E5-A CAS competition successes=%d conflicts=%d errors=%v",
			successes,
			conflicts,
			errorsByIndex,
		)
	}
}
