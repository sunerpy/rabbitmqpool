### 用于rabbitmq的连接池

fork自https://gitee.com/tym_hmm/rabbitmq-pool-go.git

一边学习一边改

1. 已实现功能：
   * 使用function option为rabbitmq设置默认值
2. 待实现功能：
   * 消息发送失败时存入本地文件
   * 捕获错误日志

## 用法

### 生产者

```go
import (
    "github.com/sunerpy/rabbitmqpool"
    "sync"
    "os"
    "fmt"
)
func main(){
    var instancePoolProducer *rabbitmqpool.RabbitPool
    var testConf = rabbitmqpool.NewAmqpConf("192.1.1.210", 5672, "root", "root", rabbitmqpool.WithRabbitType(1))
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

```
