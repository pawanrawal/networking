package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func recvFromClient(fd int) []byte {
	buf := make([]byte, 0, 128)
	maxSize := 64
	tmpBuf := make([]byte, maxSize)
	for {
		n, _, err := unix.Recvfrom(fd, tmpBuf, 0)
		check(errors.Wrapf(err, "while reading into buf from socket"))
		buf = append(buf, tmpBuf[:n]...)
		// We only get < maxSize bytes when it is the last chunk of the message.
		if n < maxSize {
			break
		}
	}
	return buf
}

func closeFd(fd int) {
	err := unix.Close(fd)
	if err != nil {
		log.Printf("error while trying to close socket: [%v], %+v", fd, err)
		return
	}
}

func parseHostPort(proxy string) (ip [4]byte, port int) {
	host, pport, err := net.SplitHostPort(proxy)
	check(errors.Wrapf(err, "while splitting proxy address"))
	iport, err := strconv.Atoi(pport)
	check(errors.Wrapf(err, "while converting port to an integer"))
	proxyIp := net.ParseIP(host)

	copy(ip[:], proxyIp)
	return ip, iport
}

func main() {
	var (
		port      = flag.Int("port", 8000, "Port to bind the socket to")
		proxy     = flag.String("proxy", "127.0.0.1:9000", "Address of the proxy server")
		cachePath = flag.String("cache", "", "Path to cache")
	)

	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Create a socket which we use to connect to proxy server.
	proxyFd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	check(errors.Wrapf(err, "while creating socket to proxy server."))

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		// Catch Interrupt and close file descriptor for proxy.
		closeFd(proxyFd)
		os.Exit(1)
	}()

	proxyIp, proxyPort := parseHostPort(*proxy)
	proxySocketAddr := &unix.SockaddrInet4{
		Port: proxyPort,
		Addr: proxyIp,
	}
	err = unix.Connect(proxyFd, proxySocketAddr)
	check(errors.Wrapf(err, "while binding socket"))

	cache := make(map[string][]byte)

	// Create a socket to bind to a port locally. Also start listening on it for connections from
	// clients.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	check(errors.Wrapf(err, "while creating socket"))
	defer unix.Close(fd)

	err = unix.Bind(fd, &unix.SockaddrInet4{
		Port: *port,
		Addr: [4]byte{127, 0, 0, 1},
	})
	check(errors.Wrapf(err, "while binding socket"))
	fmt.Printf("Created socket and bound to port: %v\n", *port)

	err = unix.Listen(fd, 1)
	check(errors.Wrapf(err, "while listening on socket"))

	for {
		// Start accepting socket connections from clients.
		clientSocketFd, _, err := unix.Accept(fd)
		check(errors.Wrapf(err, "while accepting connections on socket"))

		for {
			buf := recvFromClient(clientSocketFd)
			if len(buf) == 0 {
				// Zero bytes means the client closed the connection. Lets close the socket and
				// break from this loop so that we cna accept connection from other clients.
				closeFd(clientSocketFd)
				break
			}

			req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf)))
			check(errors.Wrapf(err, "while trying to parse request as HTTP request"))
			path := req.URL.Path
			if strings.HasPrefix(path, *cachePath) {
				b, ok := cache[path]
				if ok {
					fmt.Printf("Found cache for req with path: [%s]\n", path)
					_, err = unix.Write(clientSocketFd, b)
					check(errors.Wrapf(err, "while sending message on socket to client"))
					break
				}
			}

			_, err = unix.Write(proxyFd, buf)
			check(errors.Wrapf(err, "while sending message on socket to proxy server"))

			buf = recvFromClient(proxyFd)

			_, err = unix.Write(clientSocketFd, buf)
			check(errors.Wrapf(err, "while sending message on socket to client"))
			cache[path] = buf
		}
	}
}
