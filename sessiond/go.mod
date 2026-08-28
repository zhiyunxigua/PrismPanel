module PrismPanel-sessiond

go 1.21

require (
	PrismPanel-daemon v0.0.0
	golang.org/x/term v0.20.0
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/sys v0.20.0 // indirect

replace PrismPanel-daemon => ../daemon
