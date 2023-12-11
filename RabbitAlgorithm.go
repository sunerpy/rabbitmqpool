package rabbitmqpool

/*
连接负载处理
*/
type RabbitLoadBalance struct {
}

func NewRabbitLoadBalance() *RabbitLoadBalance {
	return &RabbitLoadBalance{}
}

/*
负载均衡方式:轮询策略
*/
func (r *RabbitLoadBalance) RoundRobin(cIndex, max int32) int32 {
	if max == 0 {
		return 0
	}
	return (cIndex + 1) % max
}
