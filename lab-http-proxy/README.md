# HTTP proxy

The program implements a reverse proxy server which can forward the requests received to another
destination server and also return the response received from the destination server to the client.
It also caches request optionally given a cache path. To start the server run

```
go build . && ./lab-http-proxy -cache /website -proxy 127.0.0.1:9000
```


## TODO

- Restarting the process can sometimes give Address already in use error, do a proper cleanup to
    avoid that.
- Support concurrency using unix select or poll API.

