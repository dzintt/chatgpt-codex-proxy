package accounts

type State struct {
	Records          []*Record        `json:"records"`
	RotationStrategy RotationStrategy `json:"rotation_strategy"`
}

type Store interface {
	Load() (State, error)
	Save(state State) error
}
