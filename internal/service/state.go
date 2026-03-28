package service

type State string

const (
	StateInitializing State = "initializing"
	StateRunning      State = "running"
	StateStopping     State = "stopping"
	StateStopped      State = "stopped"
)
