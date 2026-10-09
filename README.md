![image](https://github.com/evildecay/etcdkeeper/blob/master/logo/logo-horizontal.png)
## ETCD Keeper

> **GS1 Romania fork** of [evildecay/etcdkeeper](https://github.com/evildecay/etcdkeeper) (upstream inactive since 2024-09, last release v0.7.8).
> Changes in v0.8.0-gs1:
> * **Paged tree loading** — the server returns at most `-pagesize` nodes (default 500, max 5000) per request with a
>   `next` cursor; the UI shows a *load more ...* node. In directory mode a whole subtree collapses into one directory
>   node and the scan jumps past it, so opening a directory costs about its number of children, not its number of keys.
>   v0.7.8 never answered for the root of a 200k-key etcd; this version answers in ~10 ms per page.
> * **JSON validation in the editor** — values that look like JSON (or `*.json` keys) open in json mode; the footer shows
>   *JSON valid* / *JSON error: line L column C* while typing; saving, creating or formatting invalid JSON asks first and
>   jumps to the error.
> * **etcd v2 API removed** (etcd 3.6 has none); built with Go 1.26 and the etcd 3.6 client; no `coreos/etcd` 3.3 dependency.
> * **No process crashes**: an unreachable etcd no longer hits `log.Fatal`, requests without a session return an error
>   instead of a nil-pointer panic, and the shared root-user map is guarded by a lock (concurrent map access was fatal).
> * `-version` flag and `/version` endpoint; connect now fails when the endpoint does not answer.
> * Build with Docker only: `./build.sh` / `./release.sh <version>`.

* Lightweight etcd web client.
* Supports etcd 3.x (v3 API only).
* The server uses the etcd go client interface, and the server compiles with the etcd client package.
* Based easyui framework to achieve(easyui license [easyui website](http://www.jeasyui.com)).

## Usage
* Run etcdkeeper.exe (windows version)
* Run etcdkeeper.exe -auth (If enable etcd authentication)
* [Download other platform releases](https://github.com/evildecay/etcdkeeper/releases).
```
Usage of D:\Applications\etcdkeeper\etcdkeeper.exe:
  -auth
        use auth
  -cacert string
        verify certificates of TLS-enabled secure servers using this CA bundle (v3)
  -cert string
        identify secure client using this TLS certificate file (v3)
  -h string
        host name or ip address (default "0.0.0.0")
  -key string
        identify secure client using this TLS key file (v3)
  -p int
        port (default 8080)
  -pagesize int
        max tree nodes returned per request; the UI pages the rest with 'load more' (default 500)
  -sendMsgSize int
        ETCD client max send msg size (default 2097152)
  -sep string
        separator (default "/")
  -skiptls
        skip verify tls
  -timeout int
        ETCD client connect timeout (default 5)
  -usetls
        use tls
```
* Open your browser and enter the address: http://127.0.0.1:8080/etcdkeeper
* Right click on the tree node to add or delete.
* Get data based on etcd user permissions.
  - Just display the list according to the configured permissions, and there will be time to add the configuration permission features.
  - Each time you restart etcdkeeper, you need to enter the root username and password for each etcd server address. 
  - [enable etcdv3 authentication](https://etcd.io/docs/v3.5/op-guide/authentication/authentication/)
* Display the status information of etcd, version, data size.
* Etcd address can be modified by default to the localhost. If you change, press the Enter key to take effect.

## Features
* Etcd client view, Add, update or delete nodes.
* Content edits use the ace editor([Ace editor](https://ace.c9.io)). Support toml,ini,yaml,json,xml and so on to highlight view.
* Content format. (Currently only support json, Other types can be extended later) Thanks jim3ma for his contribution.[@jim3ma]( https://github.com/jim3ma)

## Work in progress
* Add import and export features.  **(delay)**

## Special Note
* Because the etcdv3 version uses the new storage concept, without the catalog concept, the client uses the previous default "/" delimiter to view. See the documentation for etcdv3 [clientv3 doc](https://godoc.org/github.com/coreos/etcd/clientv3).

## Compile source code and release
* `./build.sh [version]` builds static linux amd64/arm64 binaries in the `golang:1.26` container (no local Go needed).
* `./release.sh <version>` packages them with `assets/` into `release/*.tar.gz` plus `SHA256SUMS`.

## Docker
Etdkeeper official image. (https://hub.docker.com/r/evildecay/etcdkeeper)

## Screenshots
![image](https://github.com/evildecay/etcdkeeper/blob/master/screenshots/ui.png)

## Demo
![image](https://github.com/evildecay/etcdkeeper/blob/master/screenshots/ui.gif)

## License
MIT
