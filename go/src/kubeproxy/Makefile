all: server

server: main.go
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags '-w' -o kubeproxy ./main.go

clean:
	rm -f kubeproxy
