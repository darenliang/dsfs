package main

func FuseArgs(opts []string) []string {
	var args []string
	for _, arg := range opts {
		args = append(args, "-o", arg)
	}
	return args
}
