package main

import (
	"bytes"
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

	"github.com/kardianos/osext"

	"github.com/blang/semver"
	"github.com/bogdanovich/dns_resolver"
	"github.com/gorilla/websocket"
	"github.com/nu7hatch/gouuid"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"github.com/tatsushid/go-fastping"
)

const version = "0.0.15"

func selfUpdate(slug string) error {
	previous := semver.MustParse(version)
	latest, err := selfupdate.UpdateSelf(previous, slug)
	if err != nil {
		return err
	}

	if previous.Equals(latest.Version) {
		// fmt.Println("Current binary is the latest version", version)
	} else {
		fmt.Println("Update successfully done to version", latest.Version)
		fmt.Println("Release note:\n", latest.ReleaseNotes)

		restart()
	}

	return nil
}

var zu, _ = uuid.NewV4()
var addr = flag.String("addr", "localhost:80", "cc address:port")
var zonduuid = flag.String("uuid", zu.String(), "zond uuid")

type Action struct {
	ZondUuid string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	Uuid     string `json:"uuid"`
}

func main() {
	log.Printf("Started version %s", version)

	ticker := time.NewTicker(10 * time.Minute)
	go func(ticker *time.Ticker) {
		for {
			select {
			case <-ticker.C:
				if err := selfUpdate("ad/gozond"); err != nil {
					fmt.Fprintln(os.Stderr, err)
					// os.Exit(1)
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
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer ws.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				time.Sleep(time.Duration(rand.Intn(5)) * time.Second)
				restart()
				// return
			}
			// log.Printf("recv: %s", message)
			var action = new(Action)
			err = json.Unmarshal(message, &action)
			if err != nil {
				fmt.Println("error:", err)
			} else {
				fmt.Printf("%+v\n", action)
				if action.Action == "ping" {
					go ping(action.Param, action.Uuid)
				} else if action.Action == "head" {
					go head(action.Param, action.Uuid)
				} else if action.Action == "dns" {
					go dns(action.Param, action.Uuid)
				} else if action.Action == "alive" {
					ccAddr := *addr
					action.ZondUuid = *zonduuid
					js, _ := json.Marshal(action)
					// log.Println("http://"+ccAddr+"/pong", string(js))
					go post("http://"+ccAddr+"/zond/pong", string(js))
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
				log.Println("write close:", err)
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

func restart() {
	file, err := osext.Executable()
	if err != nil {
		log.Println("restart:", err)
	} else {
		err = syscall.Exec(file, os.Args, os.Environ())
		if err != nil {
			log.Fatal(err)
		}
	}
}

func ping(address string, taskuuid string) {
	ccAddr := *addr
	var action = Action{ZondUuid: *zonduuid, Action: "block", Result: "", Uuid: taskuuid}
	var js, _ = json.Marshal(action)
	var status = post("http://"+ccAddr+"/zond/task/block", string(js))

	if status != `{"status": "ok", "message": "ok"}` {
		log.Println(taskuuid, status)
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10000)) * time.Millisecond)
			ping(address, taskuuid)
		}
	} else {
		p := fastping.NewPinger()
		ra, err := net.ResolveIPAddr("ip4:icmp", address)
		if err != nil {
			fmt.Println(address+" ping failed: ", err)
			action := Action{ZondUuid: *zonduuid, Action: "result", Result: fmt.Sprintf("failed: %s", err), Uuid: taskuuid}
			js, _ := json.Marshal(action)

			post("http://"+ccAddr+"/zond/task/result", string(js))
		} else {
			p.AddIPAddr(ra)
			var received = false
			p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
				received = true
				fmt.Printf("IP Addr: %s receive, RTT: %v\n", addr.String(), rtt)

				action = Action{ZondUuid: *zonduuid, Action: "result", Result: rtt.String(), Uuid: taskuuid}
				js, _ = json.Marshal(action)

				post("http://"+ccAddr+"/zond/task/result", string(js))
			}
			p.OnIdle = func() {
				if !received {
					fmt.Println(address + " ping failed")
					action := Action{ZondUuid: *zonduuid, Action: "result", Result: "failed", Uuid: taskuuid}
					js, _ := json.Marshal(action)

					post("http://"+ccAddr+"/zond/task/result", string(js))
				}
			}
			err = p.Run()
			if err != nil {
				fmt.Println("Error", err)
			}
		}
	}
}

func dns(address string, taskuuid string) {
	ccAddr := *addr
	var action = Action{ZondUuid: *zonduuid, Action: "block", Result: "", Uuid: taskuuid}
	var js, _ = json.Marshal(action)
	var status = post("http://"+ccAddr+"/zond/task/block", string(js))

	if status != `{"status": "ok", "message": "ok"}` {
		log.Println(taskuuid, status)
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
			head(address, taskuuid)
		}
	} else {
		var resolver_address = "8.8.8.8"
		if strings.Count(address, "-") == 1 {
			s := strings.Split(address, "-")
			address, resolver_address = s[0], s[1]
		}
		resolver := dns_resolver.New([]string{resolver_address})
		// resolver := dns_resolver.NewFromResolvConf("resolv.conf")
		resolver.RetryTimes = 5

		ips, err := resolver.LookupHost(address)

		if err != nil {
			log.Println(address+" dns failed: ", err)
			action := Action{ZondUuid: *zonduuid, Action: "result", Result: fmt.Sprintf("failed: %s", err), Uuid: taskuuid}
			js, _ := json.Marshal(action)

			post("http://"+ccAddr+"/zond/task/result", string(js))
		} else {
			var s []string
			for _, ip := range ips {
				s = append(s, ip.String())
			}
			var res = strings.Join(s[:], ",")
			log.Printf("IPS: %v", res)

			action = Action{ZondUuid: *zonduuid, Action: "result", Result: res, Uuid: taskuuid}
			js, _ = json.Marshal(action)

			post("http://"+ccAddr+"/zond/task/result", string(js))
		}
	}
}

func head(address string, taskuuid string) {
	ccAddr := *addr
	var action = Action{ZondUuid: *zonduuid, Action: "block", Result: "", Uuid: taskuuid}
	var js, _ = json.Marshal(action)
	var status = post("http://"+ccAddr+"/zond/task/block", string(js))

	if status != `{"status": "ok", "message": "ok"}` {
		log.Println(taskuuid, status)
		if status == `{"status": "error", "message": "only one task at time is allowed"}` {
			time.Sleep(time.Duration(rand.Intn(10)) * time.Second)
			head(address, taskuuid)
		}
	} else {
		res, err := http.Head(address)
		if err != nil {
			// fmt.Println(address+" http head failed: ", err)
			log.Println(address+" http head failed: ", err)
			action := Action{ZondUuid: *zonduuid, Action: "result", Result: fmt.Sprintf("failed: %s", err), Uuid: taskuuid}
			js, _ := json.Marshal(action)

			post("http://"+ccAddr+"/zond/task/result", string(js))
		} else {
			headers := res.Header
			var s string
			for key, val := range headers {
				s += fmt.Sprintf("%s: %s\n", key, val)
			}
			// fmt.Printf("Headers: %v\n", s)
			log.Printf("Headers: %v", s)

			action = Action{ZondUuid: *zonduuid, Action: "result", Result: s, Uuid: taskuuid}
			js, _ = json.Marshal(action)

			post("http://"+ccAddr+"/zond/task/result", string(js))
		}
	}
}

func get(url string) string {
	response, err := http.Get(url)
	if err != nil {
		fmt.Printf("%s", err)
		// os.Exit(1)
	} else {
		defer response.Body.Close()
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			fmt.Printf("%s", err)
			os.Exit(1)
		}
		return string(contents)
	}
	return ""
}

func post(url string, jsonData string) string {
	var jsonStr = []byte(jsonData)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println(err)
		return "error"
	} else {
		defer resp.Body.Close()

		// fmt.Println("response Status:", resp.Status)
		// fmt.Println("response Headers:", resp.Header)
		body, _ := ioutil.ReadAll(resp.Body)
		return string(body)
	}
}
