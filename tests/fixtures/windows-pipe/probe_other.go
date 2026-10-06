//go:build !windows

package main

import "errors"

func probe([]string) error {
	return errors.New("probe is only available on windows")
}
