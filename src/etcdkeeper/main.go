package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"etcdkeeper/session"
	_ "etcdkeeper/session/providers/memory"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// version is set at build time: -ldflags "-X main.version=v0.8.0-gs1"
var version = "dev"

const (
	maxPageSize    = 5000             // upper bound for the "limit" request parameter
	scanBatch      = 1000             // keys fetched from etcd per range request (keys only)
	requestTimeout = 60 * time.Second // per HTTP request budget for etcd calls
)

var (
	sep            = flag.String("sep", "/", "separator")
	separator      = ""
	usetls         = flag.Bool("usetls", false, "use tls")
	skiptls        = flag.Bool("skiptls", false, "skip verify tls")
	cacert         = flag.String("cacert", "", "verify certificates of TLS-enabled secure servers using this CA bundle")
	cert           = flag.String("cert", "", "identify secure client using this TLS certificate file")
	keyfile        = flag.String("key", "", "identify secure client using this TLS key file")
	useAuth        = flag.Bool("auth", false, "use auth")
	connectTimeout = flag.Int("timeout", 5, "ETCD client connect timeout")
	sendMsgSize    = flag.Int("sendMsgSize", 2*1024*1024, "ETCD client max send msg size")
	pageSize       = flag.Int("pagesize", 500, "max tree nodes returned per request; the UI pages the rest with 'load more'")

	rootUsers   = make(map[string]*userInfo) // host:rootUser
	rootUsersMu sync.RWMutex

	sessmgr *session.Manager
	mu      sync.Mutex
)

type userInfo struct {
	host   string
	uname  string
	passwd string
}

// keyRange is a half-open etcd key range [start, end); end "" means unbounded.
type keyRange struct {
	start string
	end   string
}

type treeNode struct {
	key string
	dir bool
}

var errNoSession = errors.New("not connected to etcd, reconnect first")

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	host := flag.String("h", "0.0.0.0", "host name or ip address")
	port := flag.Int("p", 8080, "port")
	showVersion := flag.Bool("version", false, "print version and exit")

	flag.CommandLine.Parse(os.Args[1:])
	if *showVersion {
		fmt.Println(version)
		return
	}
	separator = *sep
	if *pageSize < 1 || *pageSize > maxPageSize {
		log.Fatalf("-pagesize must be between 1 and %d", maxPageSize)
	}

	http.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, version) })
	http.HandleFunc("/v3/separator", getSeparator)
	http.HandleFunc("/v3/connect", connect)
	http.HandleFunc("/v3/put", put)
	http.HandleFunc("/v3/get", get)
	http.HandleFunc("/v3/delete", del)
	// directory mode
	http.HandleFunc("/v3/getpath", getPath)

	wd, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	rootPath := filepath.Dir(wd)

	// Session management
	sessmgr, err = session.NewManager("memory", "_etcdkeeper_session", 86400)
	if err != nil {
		log.Fatal(err)
	}
	time.AfterFunc(86400*time.Second, func() {
		sessmgr.GC()
	})

	http.Handle("/", http.FileServer(http.Dir(rootPath+"/assets"))) // view static directory

	log.Printf("etcdkeeper %s listening on %s:%d\n", version, *host, *port)
	err = http.ListenAndServe(*host+":"+strconv.Itoa(*port), nil)
	if err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, map[string]interface{}{"errorCode": 500, "message": err.Error()})
}

func getRootUser(host string) (*userInfo, bool) {
	rootUsersMu.RLock()
	defer rootUsersMu.RUnlock()
	u, ok := rootUsers[host]
	return u, ok
}

func setRootUser(host string, u *userInfo) {
	rootUsersMu.Lock()
	defer rootUsersMu.Unlock()
	rootUsers[host] = u
}

func connect(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	sess := sessmgr.SessionStart(w, r)
	host := r.FormValue("host")
	uname := r.FormValue("uname")
	passwd := r.FormValue("passwd")

	if *useAuth {
		if _, ok := getRootUser(host); !ok && uname != "root" { // no root user
			writeJSON(w, map[string]interface{}{"status": "root"})
			return
		}
		if uname == "" || passwd == "" {
			writeJSON(w, map[string]interface{}{"status": "login"})
			return
		}
	}

	if uinfo, ok := sess.Get("uinfo").(*userInfo); ok {
		if host == uinfo.host && uname == uinfo.uname && passwd == uinfo.passwd {
			writeJSON(w, map[string]interface{}{"status": "running", "info": getInfo(r.Context(), host)})
			return
		}
	}

	uinfo := &userInfo{host: host, uname: uname, passwd: passwd}
	c, err := newClient(uinfo)
	if err == nil {
		defer c.Close()
		// clientv3.New does not dial, so prove the endpoint answers before keeping the session
		ctx, cancel := context.WithTimeout(r.Context(), time.Second*time.Duration(*connectTimeout))
		_, err = c.Status(ctx, host)
		cancel()
	}
	if err != nil {
		log.Println(r.Method, "v3", "connect fail:", err)
		writeJSON(w, map[string]interface{}{"status": "error", "message": fmt.Sprintf("connect %s: %v", host, err)})
		return
	}
	_ = sess.Set("uinfo", uinfo)

	if !*useAuth || uname == "root" {
		setRootUser(host, uinfo)
	}
	log.Println(r.Method, "v3", "connect success.")
	writeJSON(w, map[string]interface{}{"status": "running", "info": getInfo(r.Context(), host)})
}

// sessionClient returns an etcd client for the session's connection. The caller closes it.
func sessionClient(w http.ResponseWriter, r *http.Request) (*clientv3.Client, *userInfo, error) {
	sess := sessmgr.SessionStart(w, r)
	uinfo, ok := sess.Get("uinfo").(*userInfo)
	if !ok {
		return nil, nil, errNoSession
	}
	cli, err := newClient(uinfo)
	if err != nil {
		return nil, nil, err
	}
	return cli, uinfo, nil
}

func put(w http.ResponseWriter, r *http.Request) {
	cli, _, err := sessionClient(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	key := r.FormValue("key")
	value := r.FormValue("value")
	ttl := r.FormValue("ttl")
	log.Println("PUT", "v3", key)

	if ttl != "" {
		var sec int64
		sec, err = strconv.ParseInt(ttl, 10, 64)
		if err != nil {
			writeError(w, fmt.Errorf("invalid ttl %q: %v", ttl, err))
			return
		}
		var leaseResp *clientv3.LeaseGrantResponse
		leaseResp, err = cli.Grant(ctx, sec)
		if err == nil {
			_, err = cli.Put(ctx, key, value, clientv3.WithLease(leaseResp.ID))
		}
	} else {
		_, err = cli.Put(ctx, key, value)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	resp, err := cli.Get(ctx, key)
	if err != nil {
		writeError(w, err)
		return
	}
	data := make(map[string]interface{})
	if resp.Count > 0 {
		data["node"] = kvNode(ctx, cli, resp.Kvs[0].Key, resp.Kvs[0].Value, resp.Kvs[0].Lease, resp.Kvs[0].CreateRevision, resp.Kvs[0].ModRevision)
	}
	writeJSON(w, data)
}

func kvNode(ctx context.Context, cli *clientv3.Client, key, value []byte, lease, createRev, modRev int64) map[string]interface{} {
	return map[string]interface{}{
		"key":           string(key),
		"value":         string(value),
		"dir":           false,
		"ttl":           getTTL(ctx, cli, lease),
		"createdIndex":  createRev,
		"modifiedIndex": modRev,
	}
}

// pageParams reads the paging parameters: "after" is the opaque cursor returned as "next"
// by the previous page, "limit" overrides -pagesize (capped at maxPageSize).
func pageParams(r *http.Request) (cursor string, limit int) {
	limit = *pageSize
	if l, err := strconv.Atoi(r.FormValue("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return r.FormValue("after"), limit
}

func pagedNode(key string, nodes []treeNode, next string) map[string]interface{} {
	children := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		children = append(children, map[string]interface{}{"key": n.key, "dir": n.dir})
	}
	return map[string]interface{}{"key": key, "nodes": children, "more": next != "", "next": next}
}

func get(w http.ResponseWriter, r *http.Request) {
	key := r.FormValue("key")
	log.Println("GET", "v3", key)

	cli, uinfo, err := sessionClient(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	if r.FormValue("prefix") != "true" {
		resp, err := cli.Get(ctx, key)
		if err != nil {
			writeError(w, err)
			return
		}
		if resp.Count == 0 {
			writeError(w, errors.New("The node does not exist."))
			return
		}
		kv := resp.Kvs[0]
		writeJSON(w, map[string]interface{}{"node": kvNode(ctx, cli, kv.Key, kv.Value, kv.Lease, kv.CreateRevision, kv.ModRevision)})
		return
	}

	// list mode: every key under the prefix is a leaf, returned one page at a time
	want := keyRange{start: key, end: prefixEnd(key)}
	if key == separator {
		want = keyRange{start: "\x00", end: ""}
	}
	ranges, err := visibleRanges(ctx, uinfo, want)
	if err != nil {
		writeError(w, err)
		return
	}
	cursor, limit := pageParams(r)
	nodes, next, err := scanList(ctx, cli, ranges, cursor, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"node": pagedNode(key, nodes, next)})
}

func getPath(w http.ResponseWriter, r *http.Request) {
	key := r.FormValue("key")
	log.Println("GET", "v3", key)

	cli, uinfo, err := sessionClient(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	prefix := key + separator
	if key == separator {
		prefix = separator
	}
	ranges, err := visibleRanges(ctx, uinfo, keyRange{start: prefix, end: prefixEnd(prefix)})
	if err != nil {
		writeError(w, err)
		return
	}
	cursor, limit := pageParams(r)
	nodes, next, err := scanChildren(ctx, cli, prefix, ranges, cursor, limit)
	if err != nil {
		writeError(w, fmt.Errorf("get %s error: %v", key, err))
		return
	}
	node := pagedNode(key, nodes, next)
	node["dir"] = true

	// the directory key may carry a value of its own; only needed for the first page
	if key != separator && cursor == "" {
		presp, err := cli.Get(ctx, key)
		if err != nil {
			writeError(w, err)
			return
		}
		if presp.Count != 0 {
			kv := presp.Kvs[0]
			node["value"] = string(kv.Value)
			node["ttl"] = getTTL(ctx, cli, kv.Lease)
			node["createdIndex"] = kv.CreateRevision
			node["modifiedIndex"] = kv.ModRevision
		}
	}
	writeJSON(w, map[string]interface{}{"node": node})
}

// scanList returns up to limit keys from ranges, starting at cursor. next is the cursor for
// the following page, "" when there is nothing left.
func scanList(ctx context.Context, cli *clientv3.Client, ranges []keyRange, cursor string, limit int) ([]treeNode, string, error) {
	nodes := make([]treeNode, 0)
	for _, rg := range ranges {
		pos := maxKey(rg.start, cursor)
		for rg.end == "" || pos < rg.end {
			resp, err := cli.Get(ctx, pos, rangeEndOpt(rg.end), clientv3.WithLimit(scanBatch), clientv3.WithKeysOnly())
			if err != nil {
				return nil, "", err
			}
			for _, kv := range resp.Kvs {
				if len(nodes) == limit {
					return nodes, string(kv.Key), nil
				}
				nodes = append(nodes, treeNode{key: string(kv.Key)})
				pos = string(kv.Key) + "\x00"
			}
			if !resp.More {
				break
			}
		}
	}
	return nodes, "", nil
}

// scanChildren returns up to limit direct children of prefix (prefix = "<dir><separator>").
// Keys deeper than one level collapse into a single directory child, and the scan then jumps
// past that whole subtree, so the cost follows the number of children, not the number of keys.
func scanChildren(ctx context.Context, cli *clientv3.Client, prefix string, ranges []keyRange, cursor string, limit int) ([]treeNode, string, error) {
	nodes := make([]treeNode, 0)
	seen := make(map[string]int)
	for _, rg := range ranges {
		pos := maxKey(rg.start, cursor)
		for pos != "" && (rg.end == "" || pos < rg.end) {
			resp, err := cli.Get(ctx, pos, rangeEndOpt(rg.end), clientv3.WithLimit(scanBatch), clientv3.WithKeysOnly())
			if err != nil {
				return nil, "", err
			}
			for _, kv := range resp.Kvs {
				k := string(kv.Key)
				if k < pos { // inside a subtree already collapsed into a directory
					continue
				}
				if !strings.HasPrefix(k, prefix) || k == prefix {
					pos = k + "\x00"
					continue
				}
				child, dir := k, false
				if i := strings.Index(k[len(prefix):], separator); i >= 0 {
					child, dir = prefix+k[len(prefix):][:i], true
				}
				if idx, ok := seen[child]; ok {
					nodes[idx].dir = nodes[idx].dir || dir // a key that also has children
				} else {
					if len(nodes) == limit {
						return nodes, k, nil
					}
					seen[child] = len(nodes)
					nodes = append(nodes, treeNode{key: child, dir: dir})
				}
				if dir {
					pos = prefixEnd(child + separator)
					if pos == "" {
						break
					}
				} else {
					pos = k + "\x00"
				}
			}
			if !resp.More {
				break
			}
		}
	}
	return nodes, "", nil
}

func rangeEndOpt(end string) clientv3.OpOption {
	if end == "" {
		return clientv3.WithFromKey()
	}
	return clientv3.WithRange(end)
}

// prefixEnd returns the first key after every key starting with p; "" means unbounded.
func prefixEnd(p string) string {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}

func maxKey(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func del(w http.ResponseWriter, r *http.Request) {
	cli, _, err := sessionClient(w, r)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	key := r.FormValue("key")
	dir := r.FormValue("dir")
	log.Println("DELETE", "v3", key)

	if _, err := cli.Delete(ctx, key); err != nil {
		io.WriteString(w, err.Error())
		return
	}

	if dir == "true" {
		if _, err := cli.Delete(ctx, key+separator, clientv3.WithPrefix()); err != nil {
			io.WriteString(w, err.Error())
			return
		}
	}
	io.WriteString(w, "ok")
}

func getTTL(ctx context.Context, cli *clientv3.Client, lease int64) int64 {
	if lease == 0 {
		return 0
	}
	resp, err := cli.Lease.TimeToLive(ctx, clientv3.LeaseID(lease))
	if err != nil {
		return 0
	}
	if resp.TTL == -1 {
		return 0
	}
	return resp.TTL
}

func getSeparator(w http.ResponseWriter, _ *http.Request) {
	io.WriteString(w, separator)
}

func newClient(uinfo *userInfo) (*clientv3.Client, error) {
	endpoints := []string{uinfo.host}
	var err error

	// use tls if usetls is true
	var tlsConfig *tls.Config
	if *usetls {
		tlsInfo := transport.TLSInfo{
			CertFile:           *cert,
			KeyFile:            *keyfile,
			TrustedCAFile:      *cacert,
			InsecureSkipVerify: *skiptls,
		}
		tlsConfig, err = tlsInfo.ClientConfig()
		if err != nil {
			log.Println(err.Error())
		}
	}

	conf := clientv3.Config{
		Endpoints:          endpoints,
		DialTimeout:        time.Second * time.Duration(*connectTimeout),
		TLS:                tlsConfig,
		MaxCallSendMsgSize: *sendMsgSize,
	}
	if *useAuth {
		conf.Username = uinfo.uname
		conf.Password = uinfo.passwd
	}

	return clientv3.New(conf)
}

// visibleRanges intersects want with the key ranges the user may read, sorted and merged.
// Without auth, and for root, that is want itself.
func visibleRanges(ctx context.Context, uinfo *userInfo, want keyRange) ([]keyRange, error) {
	if !*useAuth || uinfo.uname == "root" {
		return []keyRange{want}, nil
	}
	rootUser, ok := getRootUser(uinfo.host)
	if !ok {
		return nil, errors.New("root user is not connected for " + uinfo.host)
	}
	rootCli, err := newClient(rootUser)
	if err != nil {
		return nil, err
	}
	defer rootCli.Close()

	ur, err := rootCli.UserGet(ctx, uinfo.uname)
	if err != nil {
		return nil, err
	}
	var ranges []keyRange
	for _, role := range ur.Roles {
		rr, err := rootCli.RoleGet(ctx, role)
		if err != nil {
			return nil, err
		}
		for _, p := range rr.Perm {
			pr := keyRange{start: string(p.Key), end: string(p.RangeEnd)}
			switch pr.end {
			case "":
				pr.end = pr.start + "\x00" // single key
			case "\x00":
				pr.end = "" // from key
			}
			if in, ok := intersect(pr, want); ok {
				ranges = append(ranges, in)
			}
		}
	}
	return mergeRanges(ranges), nil
}

func intersect(a, b keyRange) (keyRange, bool) {
	r := keyRange{start: maxKey(a.start, b.start)}
	switch {
	case a.end == "":
		r.end = b.end
	case b.end == "" || a.end < b.end:
		r.end = a.end
	default:
		r.end = b.end
	}
	return r, r.end == "" || r.start < r.end
}

func mergeRanges(ranges []keyRange) []keyRange {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	var out []keyRange
	for _, rg := range ranges {
		if n := len(out); n > 0 && (out[n-1].end == "" || rg.start <= out[n-1].end) {
			if out[n-1].end != "" && (rg.end == "" || rg.end > out[n-1].end) {
				out[n-1].end = rg.end
			}
			continue
		}
		out = append(out, rg)
	}
	return out
}

func getInfo(ctx context.Context, host string) map[string]string {
	info := make(map[string]string)
	uinfo, ok := getRootUser(host)
	if !ok {
		return info
	}
	rootClient, err := newClient(uinfo)
	if err != nil {
		log.Println(err)
		return info
	}
	defer rootClient.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*time.Duration(*connectTimeout))
	defer cancel()
	status, err := rootClient.Status(ctx, host)
	if err != nil {
		log.Println("get etcd status error:", err)
		return info
	}
	mems, err := rootClient.MemberList(ctx)
	if err != nil {
		log.Println("get etcd member list error:", err)
		return info
	}
	kb := 1024
	mb := kb * 1024
	gb := mb * 1024
	for _, m := range mems.Members {
		if m.ID == status.Leader {
			info["version"] = status.Version
			gn, rem1 := size(int(status.DbSize), gb)
			mn, rem2 := size(rem1, mb)
			kn, bn := size(rem2, kb)
			if gn > 0 {
				info["size"] = fmt.Sprintf("%dG", gn)
			} else if mn > 0 {
				info["size"] = fmt.Sprintf("%dM", mn)
			} else if kn > 0 {
				info["size"] = fmt.Sprintf("%dK", kn)
			} else {
				info["size"] = fmt.Sprintf("%dByte", bn)
			}
			info["name"] = m.GetName()
			break
		}
	}
	return info
}

func size(num int, unit int) (n, rem int) {
	return num / unit, num - (num/unit)*unit
}
