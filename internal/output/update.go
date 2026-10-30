package output

import "fmt"

// UpdateOutcome is what update did or found; it is not part of the JSON.
type UpdateOutcome int

const (
	UpdateCheckedAvailable UpdateOutcome = iota // --check: a newer release exists
	UpdateCheckedDevBuild                       // --check on a development build
	UpdateUpToDate                              // nothing newer to install
	UpdateAlreadyInstalled                      // --version names this version
	UpdateInstalled                             // replaced with a newer release
	UpdateDowngraded                            // replaced with an older release
	UpdateReplacedDevBuild                      // a release replaced a development build
)

// UpdateView is the result of update and update --check.
type UpdateView struct {
	Current         string        `json:"current"`
	Latest          string        `json:"latest"`
	UpdateAvailable bool          `json:"update_available"`
	Updated         bool          `json:"updated"`
	Path            string        `json:"path,omitempty"`
	Outcome         UpdateOutcome `json:"-"`
}

// Update prints the result of update.
func (p Printer) Update(v UpdateView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	var err error
	switch v.Outcome {
	case UpdateCheckedAvailable:
		_, err = fmt.Fprintf(p.Out, "simpleota %s is available (you have %s). Run: simpleota update\n", v.Latest, v.Current)
	case UpdateCheckedDevBuild:
		_, err = fmt.Fprintf(p.Out, "This is a development build; the latest release is %s.\n", v.Latest)
	case UpdateUpToDate:
		_, err = fmt.Fprintf(p.Out, "simpleota %s is up to date.\n", v.Current)
	case UpdateAlreadyInstalled:
		_, err = fmt.Fprintf(p.Out, "simpleota %s is already installed.\n", v.Current)
	case UpdateInstalled:
		_, err = fmt.Fprintf(p.Out, "Updated simpleota from %s to %s at %s.\n", v.Current, v.Latest, v.Path)
	case UpdateDowngraded:
		_, err = fmt.Fprintf(p.Out, "Downgraded simpleota from %s to %s at %s.\n", v.Current, v.Latest, v.Path)
	case UpdateReplacedDevBuild:
		_, err = fmt.Fprintf(p.Out, "Replaced development build %s with simpleota %s at %s.\n", v.Current, v.Latest, v.Path)
	}
	return err
}
