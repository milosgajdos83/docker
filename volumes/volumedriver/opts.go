package volumedriver

import (
	"errors"
	"fmt"

	"github.com/docker/docker/pkg/parsers"
)

type DriverOpts []string

var OptsKeyNotExistError = errors.New("volume driver opts - missing key")

func (d *DriverOpts) Set(key, value string) {
	*d = append(*d, fmt.Sprintf("%s=%s", key, value))
}

func (d *DriverOpts) Get(key string) (string, error) {
	for _, val := range *d {
		k, v, err := parsers.ParseKeyValueOpt(val)
		if err != nil {
			return "", err
		}

		if key == k {
			return v, nil
		}
	}

	return "", OptsKeyNotExistError
}
