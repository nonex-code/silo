package shell

// Command aliases keep familiar mc / Unix names working without duplicating
// implementations.
func init() {
	registerAlias("list", "ls")
	registerAlias("copy", "cp")
	registerAlias("move", "mv")
	registerAlias("rename", "mv")
	registerAlias("upload", "put")
	registerAlias("download", "get")
	registerAlias("remove", "rm")
}
