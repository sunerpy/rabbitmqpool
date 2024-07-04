module github.com/sunerpy/rabbitmqpool

go 1.21.0

require (
	github.com/rabbitmq/amqp091-go v1.10.0
	go.uber.org/zap v1.27.0
)

require go.uber.org/multierr v1.10.0 // indirect

replace github.com/sunerpy/rabbitmqpool => ../
