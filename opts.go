package main

import "fmt"

type FuseOpts []string

func (opts *FuseOpts) String() string {
	return fmt.Sprint(*opts)
}

func (opts *FuseOpts) Set(opt string) error {
	*opts = append(*opts, opt)
	return nil
}

func (opts *FuseOpts) Args() []string {
	var args []string
	for _, arg := range *opts {
		args = append(args, "-o", arg)
	}
	return args
}
