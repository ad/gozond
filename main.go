package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	mrand "math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ad/gocc/proto"

	"github.com/bogdanovich/dns_resolver"
	"github.com/lixiangzhong/traceroute"
	"github.com/tatsushid/go-fastping"
	"github.com/tevino/abool"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	crypto "github.com/libp2p/go-libp2p-crypto"
	"github.com/multiformats/go-multiaddr"
	p2pgrpc "github.com/paralin/go-libp2p-grpc"
	grpc "google.golang.org/grpc"
)

const version = "0.2.0"

type GrpcClient struct {
	p *p2pgrpc.GRPCProtocol
}

type ActionServer struct {
	C          chan string
	PeerID     string
	GrpcClient *GrpcClient
}

// Action struct
type Action struct {
	ZondUUID string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	UUID     string `json:"uuid"`
}

var pongStarted = abool.New()

var mode string

var zonduuid string
var resolver string
var goccAddr string
var listenAddr string
var resolverAddress string

var privKey string

func main() {
	flag.StringVar(&mode, "mode", lookupEnvOrString("MODE", mode), "mode")
	flag.StringVar(&privKey, "privKey", lookupEnvOrString("PRIVKEY", privKey), "privKey")
	flag.StringVar(&goccAddr, "gocc", lookupEnvOrString("GOCC", goccAddr), "cc address")
	flag.StringVar(&listenAddr, "listenAddr", lookupEnvOrString("LISTENADDR", listenAddr), "listen address")
	flag.StringVar(&resolver, "resolver", lookupEnvOrString("RESOLVERADDR", resolver), "resolver address")

	flag.Parse()
	log.SetFlags(0)

	log.Printf("Started version %s", version)

	if mode == "server" {
		runPublic(privKey, listenAddr)
	} else {
		if goccAddr == "" {
			log.Fatal("provide -goccAddr (or GOCC env) for client")
		}
		runPrivate(privKey, goccAddr, listenAddr)
	}
}

func runPublic(privKey, listenAddr string) {
	ctx := context.Background()
	host := setupHost(ctx, privKey, listenAddr)

	zonduuid = fmt.Sprintf("%s", host.ID())

	p := p2pgrpc.NewGRPCProtocol(ctx, host)
	c := make(chan string, 10)

	grpcClient := &GrpcClient{p: p}

	proto.RegisterActionServer(p.GetGRPCServer(), &ActionServer{C: c, PeerID: zonduuid, GrpcClient: grpcClient})
	fmt.Println("Public serving...")

	grpcClient.call(host.ID(), zonduuid)
}

func runPrivate(privKey, goccAddr, listenAddr string) {
	ctx := context.Background()
	host := setupHost(ctx, privKey, listenAddr)

	zonduuid = fmt.Sprintf("%s", host.ID())

	// Run its own server
	p := p2pgrpc.NewGRPCProtocol(ctx, host)

	grpcClient := &GrpcClient{p: p}

	proto.RegisterActionServer(p.GetGRPCServer(), &ActionServer{PeerID: zonduuid, GrpcClient: grpcClient})
	fmt.Println("private serving...")

	// Act as client, connect to public server
	addr, err := multiaddr.NewMultiaddr(goccAddr)
	if err != nil {
		log.Fatal(err)
	}

	addrInfo, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		log.Fatal(err)
	}
	if err := host.Connect(ctx, *addrInfo); err != nil {
		log.Fatal(err)
	}

	fmt.Println("make call")
	grpcClient.call(addrInfo.ID, zonduuid)
	select {}
}

func setupHost(ctx context.Context, privKeyPath, listenAddr string) host.Host {
	privBytes, err := ioutil.ReadFile(privKeyPath)
	if err != nil {
		log.Fatal(err)
	}

	privKey, err := crypto.UnmarshalPrivateKey(privBytes)
	if err != nil {
		log.Fatal(err)
	}

	// var privKey crypto.PrivKey
	// if len(privKeyStr) == 0 {
	// 	privKey, _, _ = crypto.GenerateKeyPair(crypto.ECDSA, 2048)
	// 	m, _ := crypto.MarshalPrivateKey(privKey)
	// 	encoded := crypto.ConfigEncodeKey(m)
	// 	fmt.Println("encoded libp2p key:", encoded)
	// } else {
	// 	b, _ := crypto.ConfigDecodeKey(privKeyStr)
	// 	privKey, _ = crypto.UnmarshalPrivateKey(b)
	// }

	opts := []libp2p.Option{
		libp2p.Identity(privKey),
	}

	addr, _ := multiaddr.NewMultiaddr(listenAddr)

	opts = append(opts, libp2p.ListenAddrs(addr))

	host, err := libp2p.New(ctx, opts...)
	if err != nil {
		log.Fatal(err)
	}

	peerInfo := &peer.AddrInfo{
		ID:    host.ID(),
		Addrs: host.Addrs(),
	}
	multiAddrs, err := peer.AddrInfoToP2pAddrs(peerInfo)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Address:", multiAddrs[0])
	return host
}

func (g *GrpcClient) call(destID peer.ID, ourID string) {
	log.Println(ourID, "calling", destID)

	ctx := context.Background()
	conn, err := g.p.Dial(ctx, destID, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatal("A", err)
	}
	client := proto.NewActionClient(conn)

	// id := peer.IDB58Encode(ourID)
	resp, err := client.Call(ctx, &proto.CallRequest{ZondUUID: zonduuid, Action: "ping", Param: "ya.ru", UUID: ourID})
	if err != nil {
		log.Fatal("B", err)
	}
	fmt.Printf("response: %+v", resp)
}

func (s *ActionServer) Call(ctx context.Context, req *proto.CallRequest) (*proto.CallResponse, error) {
	// id, err := peer.IDB58Decode(req.ZondUUID)
	// log.Println("hello from", id)

	// if s.C != nil {
	// 	go func(id string) {
	// 		s.C <- id
	// 	}(zonduuid)
	// }

	result := "error"

	if req.Action != "alive" {
		log.Printf("request received: %+v\n", req)
	}

	if req.Action == "ping" {
		pingCheck(req.Param, req.UUID)
		result = "success"
	} else if req.Action == "head" {
		headCheck(req.Param, req.UUID)
		result = "success"
	} else if req.Action == "dns" {
		dnsCheck(req.Param, req.UUID)
		result = "success"
	} else if req.Action == "traceroute" {
		tracerouteCheck(req.Param, req.UUID)
		result = "success"
	} else if req.Action == "alive" {
		if !pongStarted.IsSet() {
			pongStarted.Set()

			req.ZondUUID = zonduuid
			// js, _ := json.Marshal(req)

			//Post("http://"+*addr+"/zond/pong", string(js))

			pongStarted.UnSet()

			result = "success"
		}
	}
	return &proto.CallResponse{Status: result, PeerId: s.PeerID}, nil
}

func lookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func blockTask(taskuuid string) (status string) {
	// var action = &proto.CallRequest{ZondUUID: zonduuid, Action: "block", Result: "", UUID: taskuuid}
	// var js, _ = json.Marshal(action)
	status = "error on task block" //Post("http://"+*addr+"/zond/task/block", string(js))

	return status
}

func resultTask(taskuuid string, result string) (status string) {
	// var action = &proto.CallRequest{ZondUUID: zonduuid, Action: "result", Result: result, UUID: taskuuid}
	// var action = Action{ZondUUID: *zonduuid, Action: "result", Result: result, UUID: taskuuid}
	// var js, _ = json.Marshal(action)
	status = "error on task result" //Post("http://"+*addr+"/zond/task/result", string(js))

	return status
}

func pingCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(mrand.Intn(10000)) * time.Millisecond)
			pingCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println("err", taskuuid, status)
		}
	} else {
		p := fastping.NewPinger()
		ra, err := net.ResolveIPAddr("ip4:icmp", address)
		if err != nil {
			fmt.Println(address+" ping failed: ", err)

			resultTask(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			p.AddIPAddr(ra)
			var received = false
			p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
				received = true
				fmt.Printf("IP Addr: %s receive, RTT: %v\n", addr.String(), rtt)

				resultTask(taskuuid, rtt.String())
			}
			p.OnIdle = func() {
				if !received {
					fmt.Println(address + " ping failed")

					resultTask(taskuuid, "failed")
				}
			}
			err = p.Run()
			if err != nil {
				fmt.Println("Error", err)
			}
		}
	}
}

func dnsCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(mrand.Intn(10)) * time.Second)
			dnsCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
		}
	} else {
		// var resolverAddress = "8.8.8.8"
		if strings.Count(address, "-") == 1 {
			s := strings.Split(address, "-")
			address, resolverAddress = s[0], s[1]
		}
		resolver := dns_resolver.New([]string{resolverAddress})
		// resolver := dns_resolver.NewFromResolvConf("resolv.conf")
		resolver.RetryTimes = 5

		ips, err := resolver.LookupHost(address)

		if err != nil {
			log.Println(address+" dns failed: ", err)

			resultTask(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			var s []string
			for _, ip := range ips {
				s = append(s, ip.String())
			}
			var res = strings.Join(s[:], ",")
			log.Printf("IPS: %v", res)

			resultTask(taskuuid, res)
		}
	}
}

func tracerouteCheck(address string, taskuuid string) error {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(mrand.Intn(10)) * time.Second)
			return tracerouteCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
			return nil
		}
	} else {
		t := traceroute.New(address)
		//t.MaxTTL=30
		//t.Timeout=3 * time.Second
		//t.LocalAddr="0.0.0.0"
		result, err := t.Do()

		if err != nil {
			resultTask(taskuuid, fmt.Sprintf("failed: %s", err))

			return fmt.Errorf(address+" traceroute failed: ", err)
		}

		var s []string

		for _, v := range result {
			s = append(s, v.String())
		}

		var res = strings.Join(s[:], "\n")
		log.Printf("Result: %v", res)

		resultTask(taskuuid, res)

	}
	return nil
}

func headCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(mrand.Intn(10)) * time.Second)
			headCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
		}
	} else {
		resp, err := http.Head(address)
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			log.Println(address+" http head failed: ", err)

			resultTask(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			headers := resp.Header
			var res string
			for key, val := range headers {
				res += fmt.Sprintf("%s: %s\n", key, val)
			}
			log.Printf("Headers: %v", res)

			resultTask(taskuuid, res)
		}
	}
}

// Get request and return response
func Get(url string) string {
	resp, err := http.Get(url)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		contents, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			os.Exit(1)
		}
		return string(contents)
	}
	return "error"
}

// Post request with headers and return response
func Post(url string, jsonData string) string {
	var jsonStr = []byte(jsonData)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")
	// req.Header.Set("X-ZondUuid", *zonduuid)

	client := &http.Client{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return "error"
	}

	if resp.StatusCode == 429 {
		log.Printf("%s: %d", url, resp.StatusCode)
		time.Sleep(time.Duration(mrand.Intn(30)) * time.Second)
		return Post(url, jsonData)
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}
