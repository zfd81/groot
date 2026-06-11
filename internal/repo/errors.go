// internal/repo/errors.go
package repo

import "errors"

var ErrNotFound = errors.New("repo: not found")
var ErrConflict = errors.New("repo: version conflict")
