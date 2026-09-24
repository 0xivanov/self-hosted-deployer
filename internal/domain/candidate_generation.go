package domain

// CandidateGeneration identifies a bound runtime generation. RequestedState
// contains immutable configuration references, never decrypted credentials.
type CandidateGeneration struct {
	AppName        string
	AppID          string
	RequestID      string
	RequestedState string
	State          string
}
