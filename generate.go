package scout

//go:generate protoc -I=proto --go_out=. --go_opt=module=github.com/rotationalio/scout --go-grpc_out=. --go-grpc_opt=module=github.com/rotationalio/scout api.proto
