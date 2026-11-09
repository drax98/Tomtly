package extension

import "fmt"

// Describe runs an extension folder's top-level code and says what it declares, without starting it. The marketplace
// builds its catalog from this, so an entry can't claim more or less than its code does.
func Describe(dir string) (Status, error) {
	l, err := load(dir)
	if err != nil {
		return Status{}, err
	}
	if len(l.early) > 0 {
		return Status{}, fmt.Errorf("%s: %s", l.early[0][0], l.early[0][1])
	}
	return definedStatus(dir, l, nil), nil
}
