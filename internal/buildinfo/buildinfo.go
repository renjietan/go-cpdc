package buildinfo

// Target is set by -ldflags. Supported values are generic, ccu and iec.
var Target = "generic"

func WorkspaceFactor() uint64 {
	if Target == "ccu" {
		return 2
	}
	return 1
}
