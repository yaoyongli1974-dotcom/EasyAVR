package server

import (
	"github.com/easyavr/easyavr/internal/ga1400"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/notify"
	"github.com/easyavr/easyavr/internal/search"
)

// eventSink fans a newly created AI event out to semantic indexing and alert
// notifications. It satisfies the EventSink interfaces of ai, gb28181 and
// ga1400 (all structural).
type eventSink struct {
	search *search.Service
	notify *notify.Service
	gaCas  *ga1400.CascadeService
}

func (s *eventSink) OnEvent(ev model.AIEvent) {
	go func() {
		if s.search != nil {
			s.search.IndexEvent(ev)
		}
		if s.notify != nil {
			s.notify.Notify(ev)
		}
		if s.gaCas != nil {
			s.gaCas.PushEvent(ev)
		}
	}()
}
