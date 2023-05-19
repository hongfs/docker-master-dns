package main

import (
	"encoding/base64"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/miekg/dns"
	"golang.org/x/net/context"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

var Client *client.Client

var MasterIP = ""

func init() {
	docker, err := client.NewClientWithOpts(client.FromEnv)

	if err != nil {
		panic(err)
	}

	Client = docker

	MasterIP = os.Getenv("MASTER_IP")

	if MasterIP == "" {
		panic("MASTER_IP 环境变量未设置")
	}
}

func verifyName(name string) bool {
	//re := regexp.MustCompile(`^[a-f0-9]{12}\.$`)
	//
	//if re.MatchString(name) {
	//	return false
	//}

	list, err := Client.ContainerList(context.Background(), types.ContainerListOptions{})

	if err != nil {
		log.Println("获取容器列表失败", err.Error())
		return false
	}

	for _, container := range list {
		if strings.HasPrefix(container.ID, name) {
			return true
		}

		if container.Names != nil {
			for _, containerName := range container.Names {
				if containerName == name {
					return true
				}
			}
		}
	}

	return false
}

func parseQuery(m *dns.Msg) {
	for _, q := range m.Question {
		log.Printf("查询名称：%s, 查询类型：%d\n", q.Name, q.Qtype)

		if q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA {
			if verifyName(q.Name) {
				log.Println("符合容器记录", q.Name, MasterIP)

				rr, err := dns.NewRR(fmt.Sprintf("%s A %s", q.Name, MasterIP))

				if err == nil {
					m.Answer = append(m.Answer, rr)
					return
				} else {
					log.Println("NewRR 异常", err.Error())
				}
			}
		}

		query := dns.Msg{}
		query.SetQuestion(q.Name, q.Qtype)

		msg, _ := query.Pack()

		resp, err := http.Get("https://223.5.5.5/dns-query?dns=" + base64.RawURLEncoding.EncodeToString(msg))

		if err != nil {
			log.Println("请求阿里云失败", err.Error())
			return
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)

		if err != nil {
			log.Println("io.ReadAll 失败", err.Error())
			return
		}

		response := &dns.Msg{}

		err = response.Unpack(body)

		if err != nil {
			log.Println("Unpack 失败", err.Error())
			return
		}

		m.Answer = append(m.Answer, response.Answer...)

		return
	}
}

func handleDnsRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = false

	switch r.Opcode {
	case dns.OpcodeQuery:
		parseQuery(m)
	}

	w.WriteMsg(m)
}

func main() {
	dns.HandleFunc(".", handleDnsRequest)

	server := &dns.Server{
		Addr: ":53",
		Net:  "udp",
	}

	err := server.ListenAndServe()

	defer server.Shutdown()

	if err != nil {
		log.Fatalf("Failed to start server: %s\n ", err.Error())
	}
}
