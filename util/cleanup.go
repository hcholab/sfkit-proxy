package util

import (
	"fmt"
	"strings"
)

// Cleanup calls cleanup function if err is not nil,
// and appends cleanup error to err, if any.
func Cleanup(err *error, cleanup func() error) {
	errs := make([]string, 0, 2)
	if err != nil && *err != nil {
		errs = append(errs, (*err).Error())
	}
	if e := cleanup(); e != nil {
		errs = append(errs, e.Error())
	}
	if len(errs) > 0 {
		*err = fmt.Errorf(strings.Join(errs, "; "))
	}
}
