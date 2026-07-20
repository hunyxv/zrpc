package zmq

type lifecycleState uint8

const (
	stateActive lifecycleState = iota + 1
	stateDraining
	stateClosing
	stateClosed
)

// connLifecycle is protected by its owning conn's mutex.
type connLifecycle struct {
	local lifecycleState
	peer  lifecycleState
}

func newConnLifecycle() connLifecycle {
	return connLifecycle{local: stateActive, peer: stateActive}
}

func (lc connLifecycle) canOpen() bool {
	return lc.local == stateActive && lc.peer == stateActive
}

func (lc connLifecycle) canAccept() bool {
	return lc.local == stateActive && lc.peer == stateActive
}

func (lc *connLifecycle) startLocalDrain() {
	if lc.local == stateActive {
		lc.local = stateDraining
	}
}

func (lc *connLifecycle) markPeerDraining() {
	if lc.peer == stateActive {
		lc.peer = stateDraining
	}
}

func (lc *connLifecycle) startLocalClose() {
	if lc.local == stateActive || lc.local == stateDraining {
		lc.local = stateClosing
	}
}

func (lc *connLifecycle) markPeerClosing() {
	if lc.peer == stateActive || lc.peer == stateDraining {
		lc.peer = stateClosing
	}
}

func (lc *connLifecycle) markClosed() {
	lc.local = stateClosed
	lc.peer = stateClosed
}
