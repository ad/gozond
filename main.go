package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ad/gocc/proto"

	"github.com/bogdanovich/dns_resolver"
	"github.com/kardianos/osext"
	"github.com/lixiangzhong/traceroute"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/tatsushid/go-fastping"
	"github.com/tevino/abool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	// "github.com/blang/semver"
	// "github.com/rhysd/go-github-selfupdate/selfupdate"
)

const version = "0.2.0"

// func selfUpdate(slug string) error {
// 	previous := semver.MustParse(version)
// 	latest, err := selfupdate.UpdateSelf(previous, slug)
// 	if err != nil {
// 		return err
// 	}

// 	if !previous.Equals(latest.Version) {
// 		fmt.Println("Update successfully done to version", latest.Version)

// 		Restart()
// 	}

// 	return nil
// }

var (
	pongStarted     = abool.New()
	zu, _           = uuid.NewV4()
	goccAddr        string
	zonduuid        string
	resolverAddress string

	server srv
)

// Action struct
type Action struct {
	ZondUUID string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	UUID     string `json:"uuid"`
}

type srv struct {
	stream proto.Zond_MessageClient
}

func main() {
	log.Printf("Started version %s", version)

	// ticker := time.NewTicker(10 * time.Minute)
	// go func(ticker *time.Ticker) {
	// 	for {
	// 		select {
	// 		case <-ticker.C:
	// 			if err := selfUpdate("ad/gozond"); err != nil {
	// 				fmt.Fprintln(os.Stderr, err)
	// 			}
	// 		}
	// 	}
	// }(ticker)

	flag.StringVar(&goccAddr, "gocc", lookupEnvOrString("GOZOND_GOCC", goccAddr), "cc address:port")
	flag.StringVar(&zonduuid, "uuid", lookupEnvOrString("GOZOND_UUID", zu.String()), "zond uuid")
	flag.StringVar(&resolverAddress, "resolver", lookupEnvOrString("GOZOND_RESOLVER", "8.8.8.8"), "resolver address")

	flag.Parse()
	log.SetFlags(0)

	if err := initGRPC(); err != nil {
		log.Fatal(err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	<-interrupt
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

func pingCheck(address string, taskuuid string) {
	status, _ := server.Block(taskuuid)

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

			server.Result(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			p.AddIPAddr(ra)
			var received = false
			p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
				received = true
				fmt.Printf("IP Addr: %s receive, RTT: %v\n", addr.String(), rtt)

				server.Result(taskuuid, rtt.String())
			}
			p.OnIdle = func() {
				if !received {
					fmt.Println(address + " ping failed")

					server.Result(taskuuid, "failed")
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
	status, _ := server.Block(taskuuid)

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
			address, resolverAddress = s[0], s[1]
		}
		resolver := dns_resolver.New([]string{resolverAddress})
		// resolver := dns_resolver.NewFromResolvConf("resolv.conf")
		resolver.RetryTimes = 5

		ips, err := resolver.LookupHost(address)

		if err != nil {
			log.Println(address+" dns failed: ", err)

			server.Result(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			var s []string
			for _, ip := range ips {
				s = append(s, ip.String())
			}
			var res = strings.Join(s[:], ",")
			log.Printf("IPS: %v", res)

			server.Result(taskuuid, res)
		}
	}
}

func tracerouteCheck(address string, taskuuid string) {
	status, _ := server.Block(taskuuid)

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

			server.Result(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			var s []string

			for _, v := range result {
				s = append(s, v.String())
			}

			var res = strings.Join(s[:], "\n")
			log.Printf("Result: %v", res)

			server.Result(taskuuid, res)
		}
	}
}

func headCheck(address string, taskuuid string) {
	status, _ := server.Block(taskuuid)

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

			server.Result(taskuuid, fmt.Sprintf("failed: %s", err))
		} else {
			headers := resp.Header
			var res string
			for key, val := range headers {
				res += fmt.Sprintf("%s: %s\n", key, val)
			}
			log.Printf("Headers: %v", res)

			server.Result(taskuuid, res)
		}
	}
}

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
	req.Header.Set("X-ZondUuid", zonduuid)

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
	conn, err := grpc.Dial(goccAddr, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("can not connect with server %v", err)
	}

	ctx := metadata.AppendToOutgoingContext(context.Background(), "ZondUUID", zonduuid)

	stream, err := proto.NewZondClient(conn).Message(ctx)
	if err != nil {
		return fmt.Errorf("open stream error %v", err)
	}

	server = srv{
		stream: stream,
	}

	done := make(chan bool)

	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				close(done)
				return
			}
			if err != nil {
				fmt.Printf("can not receive %v", err)
			}

			if resp.Action != "alive" {
				fmt.Printf("%+v\n", resp)
			}

			if resp.Action == "ping" {
				pingCheck(resp.Param, resp.UUID)
			} else if resp.Action == "head" {
				headCheck(resp.Param, resp.UUID)
			} else if resp.Action == "dns" {
				dnsCheck(resp.Param, resp.UUID)
			} else if resp.Action == "traceroute" {
				tracerouteCheck(resp.Param, resp.UUID)
			} else if resp.Action == "alive" {
				if !pongStarted.IsSet() {
					pongStarted.Set()

					server.Pong()

					pongStarted.UnSet()
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		if err := ctx.Err(); err != nil {
			fmt.Printf("%s", err)
		}
		close(done)
	}()

	// // <-done
	// req := proto.CallRequest{Action: "test", ZondUUID: *zonduuid}
	// if err := stream.Send(&req); err != nil {
	// 	log.Fatalf("can not send %v", err)
	// }

	return nil
}

func (s *srv) Block(taskUUID string) (string, error) {
	req := proto.MessageRequest{ZondUUID: zonduuid, Action: "block", UUID: taskUUID}
	if err := s.stream.Send(&req); err != nil {
		return "", fmt.Errorf("can not send %v", err)
	}
	// TODO:
	//       create channel
	//       wait result or timeout
	//       return result and close channel

	return "ok ok", nil
}

func (s *srv) Result(taskUUID, result string) (string, error) {
	req := proto.MessageRequest{ZondUUID: zonduuid, Action: "result", Result: result, UUID: taskUUID}
	if err := s.stream.Send(&req); err != nil {
		return "", fmt.Errorf("can not send %v", err)
	}
	// TODO: retry on error

	// TODO:
	//       create channel
	//       wait result or timeout
	//       return result and close channel

	return "ok ok", nil
}

func (s *srv) Pong() (string, error) {
	req := proto.MessageRequest{ZondUUID: zonduuid, Action: "pong"}
	if err := s.stream.Send(&req); err != nil {
		return "", fmt.Errorf("can not send %v", err)
	}
	// TODO:
	//       create channel
	//       wait result or timeout
	//       return result and close channel

	return "ok ok", nil
}

func lookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}
