package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ad/gocc/proto"

	"github.com/kardianos/osext"
	"github.com/lixiangzhong/traceroute"
	"github.com/tevino/abool"
	"google.golang.org/grpc"

	"github.com/blang/semver"
	"github.com/bogdanovich/dns_resolver"
	"github.com/gorilla/websocket"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"github.com/tatsushid/go-fastping"
)

const version = "0.1.2"

var pongStarted = abool.New()

func selfUpdate(slug string) error {
	previous := semver.MustParse(version)
	latest, err := selfupdate.UpdateSelf(previous, slug)
	if err != nil {
		return err
	}

	if !previous.Equals(latest.Version) {
		fmt.Println("Update successfully done to version", latest.Version)

		Restart()
	}

	return nil
}

var zu, _ = uuid.NewV4()
var addr = flag.String("addr", "localhost:80", "cc address:port")
var grpclistenaddr = flag.String("grpclistenaddr", "localhost:8080", "grpc listen address:port")
var zonduuid = flag.String("uuid", zu.String(), "zond uuid")
var resolverAddress = flag.String("resolver", "8.8.8.8", "resolver address")

// Action struct
type Action struct {
	ZondUUID string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	UUID     string `json:"uuid"`
}

type srv struct{}

func main() {
	_ = initGRPC()

	log.Printf("Started version %s", version)

	ticker := time.NewTicker(10 * time.Minute)
	go func(ticker *time.Ticker) {
		for {
			select {
			case <-ticker.C:
				if err := selfUpdate("ad/gozond"); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
		}
	}(ticker)

	flag.Parse()
	log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/sub/tasks,zond" + *zonduuid}
	log.Printf("connecting to %s", u.String())

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"X-ZondUuid": {*zonduuid}})
	if ws != nil {
		defer ws.Close()
	}
	if err != nil {
		log.Fatal("dial:", err)
	}
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				time.Sleep(time.Duration(rand.Intn(5)) * time.Second)

				Restart()

				return
			}

			var action = new(Action)
			err = json.Unmarshal(message, &action)
			if err != nil {
				fmt.Println("unmarshal error:", err)
			} else {
				if action.Action != "alive" {
					fmt.Printf("%+v\n", action)
				}

				if action.Action == "ping" {
					pingCheck(action.Param, action.UUID)
				} else if action.Action == "head" {
					headCheck(action.Param, action.UUID)
				} else if action.Action == "dns" {
					dnsCheck(action.Param, action.UUID)
				} else if action.Action == "traceroute" {
					tracerouteCheck(action.Param, action.UUID)
				} else if action.Action == "alive" {
					if !pongStarted.IsSet() {
						pongStarted.Set()

						action.ZondUUID = *zonduuid
						js, _ := json.Marshal(action)

						Post("http://"+*addr+"/zond/pong", string(js))

						pongStarted.UnSet()
					}
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("interrupt")
			err := ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close error:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}

// Restart app
func Restart() error {
	file, error := osext.Executable()
	if error != nil {
		return error
	}

	error = syscall.Exec(file, os.Args, os.Environ())
	if error != nil {
		return error
	}

	return nil
}

func blockTask(taskuuid string) (status string) {
	var action = Action{ZondUUID: *zonduuid, Action: "block", Result: "", UUID: taskuuid}
	var js, _ = json.Marshal(action)
	status = Post("http://"+*addr+"/zond/task/block", string(js))

	return status
}

func resultTask(taskuuid string, result string) (status string) {
	var action = Action{ZondUUID: *zonduuid, Action: "result", Result: result, UUID: taskuuid}
	var js, _ = json.Marshal(action)
	status = Post("http://"+*addr+"/zond/task/result", string(js))

	return status
}

func pingCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10000)) * time.Millisecond)
			pingCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
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
			time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
			dnsCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
		}
	} else {
		// var resolverAddress = "8.8.8.8"
		if strings.Count(address, "-") == 1 {
			s := strings.Split(address, "-")
			address, *resolverAddress = s[0], s[1]
		}
		resolver := dns_resolver.New([]string{*resolverAddress})
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

func tracerouteCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
			tracerouteCheck(address, taskuuid)
		} else if status != `{"status": "error", "message": "task not found"}` {
			log.Println(taskuuid, status)
		}
	} else {
		t := traceroute.New(address)
		//t.MaxTTL=30
		//t.Timeout=3 * time.Second
		//t.LocalAddr="0.0.0.0"
		result, err := t.Do()

		if err != nil {
			log.Println(address+" traceroute failed: ", err)

			resultTask(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			var s []string

			for _, v := range result {
				s = append(s, v.String())
			}

			var res = strings.Join(s[:], "\n")
			log.Printf("Result: %v", res)

			resultTask(taskuuid, res)
		}
	}
}

func headCheck(address string, taskuuid string) {
	var status = blockTask(taskuuid)

	if status != `{"status": "ok", "message": "ok"}` {
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
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
	req.Header.Set("X-ZondUuid", *zonduuid)

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
		time.Sleep(time.Duration(rand.Intn(30)) * time.Second)
		return Post(url, jsonData)
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

func initGRPC() error {
	grpcSrv := grpc.NewServer()

	GRPCListener, err := net.Listen("tcp", *grpclistenaddr)
	if err != nil {
		return fmt.Errorf("failed to listen on the TCP network address %s, %s", *grpclistenaddr, err)
	}

	grpcServer := NewServer()
	proto.RegisterActionServer(grpcSrv, grpcServer)

	go func() {
		if err := grpcSrv.Serve(GRPCListener); err != nil {
			log.Println(fmt.Errorf("failed to serve grpc: %s", err))
		}
	}()

	return nil
}

func NewServer() *srv {
	return &srv{}
}

func (s *srv) Call(ctx context.Context, req *proto.CallRequest) (*proto.CallResponse, error) {
	if req.Action != "alive" {
		fmt.Printf("%+v\n", req)
	}

	if req.Action == "ping" {
		pingCheck(req.Param, req.UUID)
	} else if req.Action == "head" {
		headCheck(req.Param, req.UUID)
	} else if req.Action == "dns" {
		dnsCheck(req.Param, req.UUID)
	} else if req.Action == "traceroute" {
		tracerouteCheck(req.Param, req.UUID)
	} else if req.Action == "alive" {
		if !pongStarted.IsSet() {
			pongStarted.Set()

			req.ZondUUID = *zonduuid
			js, _ := json.Marshal(req)

			Post("http://"+*addr+"/zond/pong", string(js))

			pongStarted.UnSet()
		}
	}
	return &proto.CallResponse{Status: "success"}, nil
}
