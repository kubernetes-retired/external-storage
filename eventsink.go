package main

import (
	v1_core "k8s.io/client-go/1.4/kubernetes/typed/core/v1"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// TODO: This is a temporary arrangement and will be removed once all clients are moved to use the clientset.
type EventSinkImpl struct {
	Interface v1_core.EventInterface
}

func (e *EventSinkImpl) Create(event *v1.Event) (*v1.Event, error) {
	return e.Interface.CreateWithEventNamespace(event)
}

func (e *EventSinkImpl) Update(event *v1.Event) (*v1.Event, error) {
	return e.Interface.UpdateWithEventNamespace(event)
}

func (e *EventSinkImpl) Patch(event *v1.Event, data []byte) (*v1.Event, error) {
	return e.Interface.PatchWithEventNamespace(event, data)
}
