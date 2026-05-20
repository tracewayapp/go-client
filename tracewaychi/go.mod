module go.tracewayapp.com/tracewaychi

go 1.25.1

require (
	go.tracewayapp.com v1.0.3
	go.tracewayapp.com/tracewayhttp v0.4.1
)

require (
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
)

replace go.tracewayapp.com/tracewayhttp => ../tracewayhttp

replace go.tracewayapp.com => ../
