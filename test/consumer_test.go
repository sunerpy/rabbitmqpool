package test

import (
	"fmt"
	"testing"

	"github.com/sunerpy/rabbitmqpool"
	"go.uber.org/zap"
)

func TestConsume(t *testing.T) {
	Consume()
}

func Consume() {
	var instancePoolConsumer *rabbitmqpool.RabbitPool
	var err error
	testConf := rabbitmqpool.NewAmqpConf("opsx-rabbitmq-0.opsx-rabbitmq-service", 5672, "root", "rabbiT3!", rabbitmqpool.WithRabbitType(2))
	instancePoolConsumer, err = rabbitmqpool.InitPool(testConf)
	if err != nil {
		fmt.Println("Here get pool failed...start save to file...")
		return
	} else {
		nomrl := &rabbitmqpool.ConsumeReceive{
			ExchangeName: "testChange5", //队列名称
			ExchangeType: rabbitmqpool.EXCHANGE_TYPE_DIRECT,
			Route:        "textQueue5",
			QueueName:    "textQueue5",
			IsTry:        true,  //是否重试
			IsAutoAck:    false, //自动消息确认
			MaxReTry:     5,     //最大重试次数
			EventFail: func(code int, e error, data []byte) {
				fmt.Printf("error:%s", e)
			},
			EventSuccess: func(data []byte, header map[string]interface{}, retryClient rabbitmqpool.RetryClientInterface,sLogger *zap.SugaredLogger) bool { //如果返回true 则无需重试
				_ = retryClient.Ack()
				fmt.Printf("data:%s\n", string(data))
				return true
			},
		}
		instancePoolConsumer.RegisterConsumeReceive(nomrl)
		err := instancePoolConsumer.RunConsume()
		if err != nil {
			fmt.Println(err)
		}
	}
}
