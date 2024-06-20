package test

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/sunerpy/rabbitmqpool"
	// "time"
)

func TestSum(t *testing.T) {
	s1 := []int{1, 2}
	s2 := s1
	s2 = append(s2, 3)
	sliceRise(s1)
	sliceRise(s2)
	fmt.Println(s1, s2)
	fmt.Printf("s1 type is %T and s2 type is %T",s1,s2)
}
func sliceRise(s []int) {
	s = append(s, 0)
	for i := range s {
		s[i]++
		fmt.Println("i is", s[i])
	}
}
func TestProduct(t *testing.T) {
	waitall()
}

var testConf = rabbitmqpool.NewAmqpConf("128.199.137.210", 45672, "root", "rabbiT3!", rabbitmqpool.WithRabbitType(1))

func waitall() {
	var instancePoolProducer *rabbitmqpool.RabbitPool
	var wg sync.WaitGroup
	var err error
	go rabbitmqpool.TmpMain()
	localFile := "localdata.txt"
	instancePoolProducer, err = rabbitmqpool.InitPool(testConf)
	if err != nil || instancePoolProducer == nil {
		fmt.Println("Here get pool failed...start save to file...")
		os.Exit(1)
	}
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

var jsonData string = `
        {
                "op": "delivery_key_manu",
                "message": {
                        "node_info": [
                                {
                                        "node_key": "ssh-rsa 4\n",
                                        "node_host_name": "TVM04"
                                },
                                {
                                        "node_key": "ssh-rsa 05\n",
                                        "node_host_name": "TVM05"
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
                        "task_id": "69f6-35c2-11ee-855b-0050568c5f9f"
                }
        }
        `
