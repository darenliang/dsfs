package main

import "fmt"

type fuseOpts []string

func (opts *fuseOpts) String() string {
	return fmt.Sprint(*opts)
}

func (opts *fuseOpts) Set(opt string) error {
	*opts = append(*opts, opt)
	return nil
}

func (opts *fuseOpts) Args() []string {
	var args []string
	for _, arg := range *opts {
		args = append(args, "-o", arg)
	}
	return args
}
