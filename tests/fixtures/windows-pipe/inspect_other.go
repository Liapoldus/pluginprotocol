//go:build !windows

package main

import "errors"

func inspect([]string) error {
	return errors.New("inspect is only available on windows")
}
