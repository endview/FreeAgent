package harness

// An entry can observe this request's progress without replacing the Owner's
// event stream or subscribing to other conversations.
type runEventObserverKey struct{}
