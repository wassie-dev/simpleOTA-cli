package output

// UpdateView is the result of update and update --check.
type UpdateView struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	Path            string `json:"path,omitempty"`
}
