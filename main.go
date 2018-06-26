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
	"time"

	"github.com/gorilla/websocket"
	"github.com/nu7hatch/gouuid"
	"github.com/tatsushid/go-fastping"
)

var addr = flag.String("addr", "localhost:80", "http service address")
var zonduuid, _ = uuid.NewV4()

type Action struct {
	ZondUuid string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	Uuid     string `json:"uuid"`
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/sub/tasks"}
	log.Printf("connecting to %s", u.String())

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"X-ZondUuid": {zonduuid.String()}})
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
				return
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

func ping(address string, taskuuid string) {
	ccAddr := *addr
	var action = Action{ZondUuid: zonduuid.String(), Action: "block", Result: "", Uuid: taskuuid}
	var js, _ = json.Marshal(action)
	var status = post("http://"+ccAddr+"/task/block", string(js))

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
			action := Action{ZondUuid: zonduuid.String(), Action: "result", Result: fmt.Sprintf("failed: %s", err), Uuid: taskuuid}
			js, _ := json.Marshal(action)

			post("http://"+ccAddr+"/task/result", string(js))
		} else {
			p.AddIPAddr(ra)
			var received = false
			p.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
				received = true
				fmt.Printf("IP Addr: %s receive, RTT: %v\n", addr.String(), rtt)

				action = Action{ZondUuid: zonduuid.String(), Action: "result", Result: rtt.String(), Uuid: taskuuid}
				js, _ = json.Marshal(action)

				post("http://"+ccAddr+"/task/result", string(js))
			}
			p.OnIdle = func() {
				if !received {
					fmt.Println(address + " ping failed")
					action := Action{ZondUuid: zonduuid.String(), Action: "result", Result: "failed", Uuid: taskuuid}
					js, _ := json.Marshal(action)

					post("http://"+ccAddr+"/task/result", string(js))
				}
			}
			err = p.Run()
			if err != nil {
				fmt.Println("Error", err)
			}
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
		panic(err)
	}
	defer resp.Body.Close()

	// fmt.Println("response Status:", resp.Status)
	// fmt.Println("response Headers:", resp.Header)
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}
