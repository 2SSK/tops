package spec

// Defaults fills every field the operator left empty with its documented default
func Defaults(c *Cluster) {
	setDefault(&c.Temporal.LogLevel, "info")
	setDefault(&c.Secrets, "secrets.env")
	setDefault(&c.DynamicConfig, "dynamicconfig.yaml")
	setDefault(&c.SSH.Port, 22)
	setDefault(&c.SSH.KnownHosts, "~/.ssh/known_hosts")

	if pg := c.Persistence.Postgres; pg != nil {
		setDefault(&pg.Port, 5432)
		setDefault(&pg.AdminDatabase, "postgres")
	}
}

// setDefault sets *field to value if it holds its type's zero value.
func setDefault[T comparable](field *T, value T) {
	var zero T
	if *field == zero {
		*field = value
	}
}
