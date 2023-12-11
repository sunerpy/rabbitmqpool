package test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/sunerpy/rabbitmqpool"
	// "time"
)

func TestProduct(t *testing.T) {
	waitall()
}

/*
@param rabbitType int: 0为生产者,1为消费者

@param vHost string: 虚拟主机,默认为'/'

//! ping need: sysctl -w net.ipv4.ping_group_range="0 2147483647"
*/
func InitRabbitPool(rabbitType int, vHost string) (*rabbitmqpool.RabbitPool, error) {
	var instancePool *rabbitmqpool.RabbitPool
	var err error
	if vHost == "" {
		vHost = "/"
	}
	pcConf := &rabbitmqpool.AmqpConfig{
		Host:       "192.168.3.12",
		Port:       5672,
		User:       "root",
		Password:   "root",
		RabbitType: rabbitType,
		VHost:      vHost,
	}
	testConf := &rabbitmqpool.AmqpConfig{
		Host:       "128.199.137.210",
		Port:       45672,
		User:       "root",
		Password:   "rabbiT3!",
		RabbitType: rabbitType,
		VHost:      vHost,
	}
	pcFlag := true
	// pcFlag := false
	if !pcFlag {
		instancePool, err = instancePool.InitPool(testConf)
	} else {
		instancePool, err = instancePool.InitPool(pcConf)
	}
	if err != nil {
		fmt.Println("Connect rabbitmq failed.")
		instancePool = nil
		// panic("Connect rabbitmq failed.")
	}
	return instancePool, err
}

var instancePoolProducer *rabbitmqpool.RabbitPool

func waitall() {
	var wg sync.WaitGroup
	// 定义 ClaimResult0 结构体变量用于保存解析后的数据
	var result ClaimResult0
	go rabbitmqpool.TmpMain()
	localFile := "localdata.txt"
	// 解析 JSON 数据到结构体
	err := json.Unmarshal([]byte(jsonData), &result)
	if err != nil {
		fmt.Println("解析 JSON 失败：", err)
		return
	}
	instancePoolProducer, err = InitRabbitPool(1, "")
	if err != nil {
		fmt.Println("Here get pool failed...start save to file...")
		//todo  获取连接池失败?
	}
	fmt.Println("Here test stop rabbit...")
	// time.Sleep(10*time.Second)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()
			data := rabbitmqpool.GetRabbitMqDataFormat("testChange5", rabbitmqpool.EXCHANGE_TYPE_TOPIC, "textQueue5", "", "这里是数据", localFile)
			data.Data = fmt.Sprintf("update num is %v", jsonData)
			err := instancePoolProducer.Push(data)
			if err != nil {
				fmt.Printf("err is %v", err)
			}
		}(i)
	}
	wg.Wait()
}

//wg.Add(1)
//go func() {
//      fmt.Println("aaaaaaaaaaaaaaaaaaaaaa")
//      defer wg.Done()
//      runtime.SetMutexProfileFraction(1)  // 开启对锁调用的跟踪
//      runtime.SetBlockProfileRate(1)      // 开启对阻塞操作的跟踪
//      err:= http.ListenAndServe("0.0.0.0:8080", nil)
//      fmt.Println(err)
//}()

// NodeKey 结构体用于表示 node_keys 数组中的每个元素的数据结构
type NodeKey struct {
	NodeKey      string `json:"node_key"`
	NodeHostName string `json:"node_host_name"`
}

// ClaimResult0 结构体用于表示 claim_result0 的数据结构
type ClaimResult0 struct {
	Op      string `json:"op"`
	Message struct {
		NodeInfo []NodeKey `json:"node_info"`
		IP       string    `json:"ip"`
		HostName string    `json:"host_name"`
		OSType   string    `json:"os_type"`
		NewPass  []string  `json:"new_pass"`
		IPVar    []string  `json:"ip_var"`
		TaskID   string    `json:"task_id"`
	} `json:"message"`
}

var jsonData string = `
        {
                "op": "delivery_key_manu",
                "message": {
                        "node_info": [
                                {
                                        "node_key": "ssh-rsa 4\n",
                                        "node_host_name": "T186PC11VM04"
                                },
                                {
                                        "node_key": "ssh-rsa 05\n",
                                        "node_host_name": "T186PC09VM05"
                                }
                        ],
                        "ip": "192.1.129.140",
                        "host_name": "p36",
                        "os_type": "LINUX AS8 U5",
                        "new_pass": [
                                "fsafd6",
                        ],
                        "ip_var": [
                                "192.1.129.140",
                                "p36",
                                "LINUX AS8 U5",
                                "",
                                "",
                                "22",
                                "ssh",
                                "",
                                ""
                        ],
                        "task_id": "67a3d9f6-35c2-11ee-855b-0050568c5f9f"
                }
        }
        `
