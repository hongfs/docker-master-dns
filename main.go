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
	"math/rand"
	"net/http"
	"os"
	"strings"
)

var Client *client.Client

// MasterIP 为主服务器 IP 地址
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

	log.Printf("MasterIP: %s\n", MasterIP)
}

// verifyDockerName 验证容器名称在 Master 上是否存在
func verifyDockerName(name string) bool {
	list, err := Client.ContainerList(context.Background(), types.ContainerListOptions{})

	if err != nil {
		log.Println("获取容器列表失败", err.Error())
		return false
	}

	for _, container := range list {
		// 过滤掉非运行中的容器
		if !strings.HasPrefix(container.Status, "Up") {
			continue
		}

		// 判断是不是以容器 ID 开头的，容器 ID 是 64 位的，这里判断必须要大于 12 位（提升容错率）
		if strings.HasPrefix(container.ID, name) && len(name) >= 12 {
			return true
		}

		if container.ID == name {
			return true
		}

		if container.Names != nil {
			for _, containerName := range container.Names {
				// 容器自定义名称，是以 / 开头的。例如：/dns
				if containerName[1:] == name {
					return true
				}
			}
		}
	}

	return false
}

// verifyLocalName 验证容器名称是否是本地容器
// 这里的本地是请求 DNS 的客户端，名称只获取环境变量的设置，不考虑是否真实存在
// 例如：LOCAL_DOCKER_NAMES=dns,nginx
func verifyLocalName(name string) bool {
	names := os.Getenv("LOCAL_DOCKER_NAMES")

	if names == "" {
		return false
	}

	for _, localName := range strings.Split(names, ",") {
		if localName == name {
			return true
		}
	}

	return false
}

// getDefaultRR 获取默认的 DNS 记录，这里使用了 AliDNS 的公共 DNS 服务
func getDefaultRR(q dns.Question) ([]dns.RR, error) {
	query := dns.Msg{}
	query.SetQuestion(q.Name, q.Qtype)

	msg, err := query.Pack()

	if err != nil {
		return nil, err
	}

	servers := [...]string{
		"223.5.5.5", // 权重 90
		"223.5.5.5",
		"223.5.5.5",
		"223.6.6.6", // 权重 30
	}

	server := servers[rand.Intn(len(servers))]

	dnsUrl := fmt.Sprintf("https://%s/dns-query?dns=%s", server, base64.RawURLEncoding.EncodeToString(msg))

	resp, err := http.Get(dnsUrl)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	response := &dns.Msg{}

	err = response.Unpack(body)

	if err != nil {
		return nil, err
	}

	return response.Answer, nil
}

// parseQuery 解析 DNS 请求
func parseQuery(w dns.ResponseWriter, m *dns.Msg) {
	for _, q := range m.Question {
		log.Printf("查询名称：%s, 查询类型：%d\n", q.Name, q.Qtype)

		name := q.Name

		// 去掉最后的 .
		if strings.HasSuffix(name, ".") {
			name = name[:len(name)-1]
		}

		if len(name) > 0 && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA) {
			if verifyDockerName(name) {
				log.Println("符合容器记录", name, MasterIP)

				rr, _ := dns.NewRR(fmt.Sprintf("%s %s %s", q.Name, dns.Type(q.Qtype).String(), MasterIP))

				if rr != nil {
					m.Answer = append(m.Answer, rr)
					return
				}
			} else if verifyLocalName(q.Name) {
				remoteAddr := strings.Split(w.RemoteAddr().String(), ":")[0]

				if strings.HasPrefix(remoteAddr, "[") {
					remoteAddr = remoteAddr[1 : len(remoteAddr)-1]
				}

				rr, _ := dns.NewRR(fmt.Sprintf("%s %s %s", q.Name, dns.Type(q.Qtype).String(), remoteAddr))

				if rr != nil {
					m.Answer = append(m.Answer, rr)
					return
				}
			}
		}

		rr, err := getDefaultRR(q)

		if err != nil {
			log.Println("获取默认记录失败", err.Error())
			return
		}

		m.Answer = append(m.Answer, rr...)

		return
	}
}

// handleDnsRequest 处理 DNS 请求
func handleDnsRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = false

	switch r.Opcode {
	case dns.OpcodeQuery:
		parseQuery(w, m)
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
