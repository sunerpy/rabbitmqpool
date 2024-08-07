package rabbitmqpool

import (
	"context"
	rand2 "crypto/rand"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

var (
	ErrFooAckNil = errors.New("ack data nil")
)

const (
	DEFAULT_MAX_CONNECTION      = 15 //rabbitmq tcp 最大连接数
	DEFAULT_MAX_CONSUME_CHANNEL = 25 //最大消费channel数(一般指消费者)
	DEFAULT_MAX_CONSUME_RETRY   = 5  //消费者断线重连最大次数
	DEFAULT_PUSH_MAX_TIME       = 5  //最大重发次数

	DEFAULT_MAX_PRODUCT_RETRY = 5 //生产者断线重连最大次数

	//轮循-连接池负载算法
	LOAD_BALANCE_ROUND = 1
)

const (
	RABBITMQ_TYPE_PUBLISH = 1 //生产者
	RABBITMQ_TYPE_CONSUME = 2 //消费者

	DEFAULT_RETRY_MIN_RANDOM_TIME = 5000 //最小重试时间机数

	DEFAULT_RETRY_MAX_RADNOM_TIME = 15000 //最大重试时间机数

)

const (
	EXCHANGE_TYPE_FANOUT = "fanout" //  Fanout：广播，将消息交给所有绑定到交换机的队列
	EXCHANGE_TYPE_DIRECT = "direct" //Direct：定向，把消息交给符合指定routing key 的队列
	EXCHANGE_TYPE_TOPIC  = "topic"  //Topic：通配符，把消息交给符合routing pattern（路由模式） 的队列
)

/*
错误码
*/
const (
	RCODE_PUSH_MAX_ERROR                    = 501 //发送超过最大重试次数
	RCODE_GET_CHANNEL_ERROR                 = 502 //获取信道失败
	RCODE_CHANNEL_QUEUE_EXCHANGE_BIND_ERROR = 503 //交换机/队列/绑定失败
	RCODE_CONNECTION_ERROR                  = 504 //连接失败
	RCODE_PUSH_ERROR                        = 505 //消息推送失败
	RCODE_CHANNEL_CREATE_ERROR              = 506 //信道创建失败
	RCODE_RETRY_MAX_ERROR                   = 507 //超过最大重试次数

)

type amqpConfig struct {
	host       string //rabbitmq主机
	port       int    //端口号
	user       string
	password   string
	rabbitType int                //初始化类型,1代表生产者,2为消费者
	vHost      string             //rabbitmq使用的vhost,默认为/
	sLogger    *zap.SugaredLogger //日志
}

type funcOption func(*amqpConfig)

func WithRabbitvHost(s string) funcOption {
	return func(o *amqpConfig) {
		o.vHost = s
	}
}

/*
@param i int: 指rabbitmq类型,1为生产者,2为消费者
*/
func WithRabbitType(i int) funcOption {
	return func(o *amqpConfig) {
		o.rabbitType = i
	}
}

func WithRabbitLogger(s *zap.SugaredLogger) funcOption {
	return func(o *amqpConfig) {
		o.sLogger = s
	}
}

func NewAmqpConf(host string, port int, user string, password string, opts ...funcOption) *amqpConfig {
	cnf := &amqpConfig{
		host:       host,
		port:       port,
		user:       user,
		password:   password,
		rabbitType: 1,
		vHost:      "/",
	}
	for _, opt := range opts {
		opt(cnf)
	}
	return cnf
}

type RetryClientInterface interface {
	Push(pushData []byte) *RabbitMqError
	Ack() error
}

/*
重试工具
*/
type retryClient struct {
	channel          *amqp.Channel
	data             *amqp.Delivery
	header           map[string]interface{}
	deadExchangeName string
	deadQueueName    string
	deadRouteKey     string
	pool             *RabbitPool
	receive          *ConsumeReceive
}

func newRetryClient(channel *amqp.Channel, data *amqp.Delivery, header map[string]interface{}, deadExchangeName string, deadQueueName string, deadRouteKey string, pool *RabbitPool, receive *ConsumeReceive) *retryClient {
	return &retryClient{channel: channel, data: data, header: header, deadExchangeName: deadExchangeName, deadQueueName: deadQueueName, deadRouteKey: deadRouteKey, pool: pool, receive: receive}
}

func (r *retryClient) Ack() error {
	//如果是非自动确认消息 手动进行确认
	if !r.receive.IsAutoAck {

		if r.data != nil {
			return r.data.Ack(true)
		}
		return ErrFooAckNil
	}
	return nil
}

// ! 超出尝试次数的放入死信队列

func (r *retryClient) Push(pushData []byte) *RabbitMqError {
	if r.channel == nil {
		return NewRabbitMqError(RCODE_GET_CHANNEL_ERROR, fmt.Sprintf("获取队列 %s 的消费通道失败", r.deadQueueName), fmt.Sprintf("获取队列 %s 的消费通道失败", r.deadQueueName))
	}

	var retryNums int32
	if retryNum, ok := r.header["retry_nums"]; !ok {
		retryNums = 0
	} else {
		retryNums = retryNum.(int32)
	}

	retryNums++

	switch {
	case retryNums >= r.receive.MaxReTry:
		if r.receive.EventFail != nil {
			r.receive.EventFail(RCODE_RETRY_MAX_ERROR, NewRabbitMqError(RCODE_RETRY_MAX_ERROR, "The maximum number of retries exceeded. Procedure", ""), pushData)
		}
	default:
		go r.retryMessage(retryNums, pushData)
	}

	return nil
}

func (r *retryClient) retryMessage(tryNum int32, pushD []byte) {
	time.Sleep(time.Millisecond * 200)

	header := make(map[string]interface{}, 1)
	header["retry_nums"] = tryNum

	expirationTime, errs := RandomAround(r.pool.minRandomRetryTime, r.pool.maxRandomRetryTime)
	if errs != nil {
		expirationTime = 5000
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := r.channel.PublishWithContext(ctx, r.deadExchangeName, r.deadRouteKey, false, false, amqp.Publishing{
		ContentType:  "text/plain",
		Body:         pushD,
		Expiration:   strconv.FormatInt(expirationTime, 10),
		Headers:      r.header,
		DeliveryMode: amqp.Persistent,
	})

	if err != nil && r.receive.EventFail != nil {
		r.receive.EventFail(RCODE_RETRY_MAX_ERROR, NewRabbitMqError(RCODE_RETRY_MAX_ERROR, "The maximum number of retries exceeded. Procedure", ""), pushD)
	}
}

/*
错误返回
*/
type RabbitMqError struct {
	Code    int
	Message string
	Detail  string
}

func (e RabbitMqError) Error() string {
	return fmt.Sprintf("Exception (%d) Reason: %q", e.Code, e.Message)
}

func NewRabbitMqError(code int, message string, detail string) *RabbitMqError {
	return &RabbitMqError{Code: code, Message: message, Detail: detail}
}

/*
消费者注册接收数据
*/
type ConsumeReceive struct {
	ExchangeName string                                                                                  //交换机
	ExchangeType string                                                                                  //交换机类型
	Route        string                                                                                  //路由
	QueueName    string                                                                                  //队列名称
	EventSuccess func(data []byte, header map[string]interface{}, retryClient RetryClientInterface,sLogger *zap.SugaredLogger) bool //成功事件回调
	EventFail    func(int, error, []byte)                                                                //失败回调

	IsTry     bool  //是否重试
	MaxReTry  int32 //最大重式次数
	IsAutoAck bool  //是否自动确认
}

type RetryToolInterface interface {
	push()
}

type RetryTool struct {
	// channel *amqp.Channel
}

func (r *RetryTool) push() {

}

/*
单个rabbitmq channel
*/
type rChannel struct {
	ch    *amqp.Channel
	index int32
}

type rConn struct {
	conn  *amqp.Connection
	index int32
}

type RabbitPool struct {
	minRandomRetryTime int64
	maxRandomRetryTime int64

	maxConnection int32 // 最大连接数量
	pushMaxTime   int   //最大重发次数

	connectionIndex   int32 //记录当前使用的连接
	connectionBalance int   //连接池负载算法

	channelPool map[int64]*rChannel //channel信道池
	connections map[int][]*rConn    // rabbitmq连接池

	channelLock    sync.RWMutex //信道池锁
	connectionLock sync.Mutex   //连接锁

	rabbitLoadBalance *RabbitLoadBalance //连接池负载模式(生产者)

	consumeMaxChannel   int32             //消费者最大信道数一般指消费者
	consumeReceive      []*ConsumeReceive //消费者注册事件
	consumeMaxRetry     int32             //消费者断线重连最大次数
	consumeCurrentRetry int32             //当前重连次数

	productMaxRetry     int32 //生产者重连次数
	productCurrentRetry int32
	pushCurrentRetry    int32 //当前推送重连交数

	clientType int //客户端类型 生产者或消费者 默认为生产者

	errorChanel chan *amqp.Error //错误捕捉channel

	connectStatus bool

	host        string //服务ip
	port        int    //服务端口
	user        string //用户名
	password    string //密码
	virtualHost string // 默认为/
	sLogger    *zap.SugaredLogger
}

/*
初始化生产者
*/
func NewProductPool() *RabbitPool {
	return newRabbitPool(RABBITMQ_TYPE_PUBLISH)
}

/*
初始化消费者
*/
func NewConsumePool() *RabbitPool {
	return newRabbitPool(RABBITMQ_TYPE_CONSUME)
}

func newRabbitPool(clientType int) *RabbitPool {
	return &RabbitPool{
		minRandomRetryTime: DEFAULT_RETRY_MIN_RANDOM_TIME,
		maxRandomRetryTime: DEFAULT_RETRY_MAX_RADNOM_TIME,

		clientType:          clientType,
		consumeMaxChannel:   DEFAULT_MAX_CONSUME_CHANNEL,
		maxConnection:       DEFAULT_MAX_CONNECTION,
		pushMaxTime:         DEFAULT_PUSH_MAX_TIME,
		connectionBalance:   LOAD_BALANCE_ROUND,
		connectionIndex:     0,
		consumeMaxRetry:     DEFAULT_MAX_CONSUME_RETRY,
		consumeCurrentRetry: 0,
		productMaxRetry:     DEFAULT_MAX_PRODUCT_RETRY,
		pushCurrentRetry:    0,
		connectStatus:       false,
		connections:         make(map[int][]*rConn, 2),
		channelPool:         make(map[int64]*rChannel, 1),
		rabbitLoadBalance:   NewRabbitLoadBalance(),
		errorChanel:         make(chan *amqp.Error),
	}
}

/*
设置消费者最大信道数
*/
func (r *RabbitPool) SetMaxConsumeChannel(maxConsume int32) {
	r.consumeMaxChannel = maxConsume
}

/*
设置最大连接数
*/
func (r *RabbitPool) SetMaxConnection(maxConnection int32) {
	r.maxConnection = maxConnection
}

/*
设置随时重试时间
避免同一时刻一次重试过多
*/
func (r *RabbitPool) SetRandomRetryTime(min, max int64) {
	r.minRandomRetryTime = min
	r.maxRandomRetryTime = max
}

/*
设置连接池负载算法,默认轮循
*/
func (r *RabbitPool) SetConnectionBalance(balance int) {
	r.connectionBalance = balance
}

func (r *RabbitPool) GetHost() string {
	return r.host
}

func (r *RabbitPool) GetPort() int {
	return r.port
}

/*
连接rabbitmq
*/
func (r *RabbitPool) Connect(amqpconfig *amqpConfig) error {
	r.host = amqpconfig.host
	r.port = amqpconfig.port
	r.user = amqpconfig.user
	r.password = amqpconfig.password
	r.virtualHost = amqpconfig.vHost
	r.sLogger = amqpconfig.sLogger
	return r.initConnections(false)
}

func (r *RabbitPool) IsHealthy() bool {
	rc := r.getConnection()
	if rc == nil {
		return false
	}
	if rc.conn != nil && !rc.conn.IsClosed() {
		return true
	}
	return false
}

func monitorPool(pool *RabbitPool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered from panic in monitorPool: %v\n", r)
			go monitorPool(pool) // 重启监控 goroutine
		}
	}()
	for {
		time.Sleep(30 * time.Second) // 每隔 30 秒检查一次
		if !pool.IsHealthy() {
			fmt.Println("Connection pool is unhealthy, reconnecting...")
			err := pool.initConnections(false)
			if err != nil {
				fmt.Println("Failed to reconnect:", err)
			} else {
				fmt.Println("Reconnected successfully")
			}
		}
	}
}

/*
注册消费接收
*/
func (r *RabbitPool) RegisterConsumeReceive(consumeReceive *ConsumeReceive) {
	if consumeReceive != nil {
		r.consumeReceive = append(r.consumeReceive, consumeReceive)
	}
}

/*
消费者
*/
func (r *RabbitPool) RunConsume() error {
	r.clientType = RABBITMQ_TYPE_CONSUME
	if len(r.consumeReceive) == 0 {
		return errors.New("未注册消费者事件")
	}
	rConsume(r)
	return nil
}
func (r *RabbitPool) Close() error {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("rabbitmq close error:", err)
		}
	}()
	r.connectionLock.Lock()
	defer r.connectionLock.Unlock()
	for _, conn := range r.connections {
		for _, channel := range conn {
			channel.conn.Close()
		}
	}
	return nil
}

/*
发送消息
*/

func (r *RabbitPool) PushWithContext(ctx context.Context, data *RabbitMqData) *RabbitMqError {
	return rPushWithCtx(ctx, r, data, 1)
}

func rPushWithCtx(ctx context.Context, pool *RabbitPool, data *RabbitMqData, sendTime int) *RabbitMqError {
	if sendTime >= pool.pushMaxTime {
		fmt.Println("//todel debug send failed! start write localdata to file...")
		writeToLocalFile(data.Data, data.Localfile)
		return NewRabbitMqError(RCODE_PUSH_MAX_ERROR, "重试超过最大次数", "")
	}

	pool.channelLock.Lock()
	defer pool.channelLock.Unlock()

	conn := pool.getConnection()
	conn, isTry := tryConn(pool, conn, 0, false, data.ExchangeName, data.ExchangeType, data.QueueName, data.Route)
	rChannels, err := pool.getChannelQueueReset(conn, data.ExchangeName, data.ExchangeType, data.QueueName, data.Route, false, 0, isTry)
	if err != nil {
		return NewRabbitMqError(RCODE_GET_CHANNEL_ERROR, "获取信道失败", err.Error())
	}

	err = rChannels.ch.PublishWithContext(ctx, data.ExchangeName, data.Route, false, false, amqp.Publishing{
		ContentType:  "text/plain",
		Body:         []byte(data.Data),
		DeliveryMode: amqp.Persistent, //持久化到磁盘
	})
	if err != nil {
		if ctx.Err() != nil {
			// 如果 ctx 被取消或超时，直接返回
			return NewRabbitMqError(RCODE_CONNECTION_ERROR, "上下文取消或超时", ctx.Err().Error())
		}
		// 如果消息发送失败, 重试发送
		time.Sleep(time.Second * 2)
		sendTime++
		return rPushWithCtx(ctx, pool, data, sendTime)
	}

	return nil
}

func (r *RabbitPool) Push(data *RabbitMqData) *RabbitMqError {
	return rPush(r, data, 1)
}

/*
获取当前连接

1.这里可以做负载算法, 默认使用轮循
*/
//TODO 连接建立失败时,返回异常,避免下标越界
func (r *RabbitPool) getConnection() *rConn {
	r.connectionIndex = r.connectionIndex % int32(r.maxConnection)
	if len(r.connections[r.clientType]) == 0 {
		return nil
	}
	changeConnectionIndex := atomic.LoadInt32(&r.connectionIndex)
	currentIndex := r.rabbitLoadBalance.RoundRobin(changeConnectionIndex, r.maxConnection)
	currentNum := currentIndex - changeConnectionIndex
	atomic.AddInt32(&r.connectionIndex, currentNum)
	if currentIndex >= int32(len(r.connections[r.clientType])) {
		return nil
	}
	return r.connections[r.clientType][currentIndex]
}

/*
获取信道

1.如果当前信道池不存在则创建

2.如果信息池存在则直接获取

3.每个连接池中连接维护一组信道

@param channelName string 信息道名称
*/
// func (r *RabbitPool) getChannelQueue(conn *rConn, exChangeName string, exChangeType string, queueName string, route string, isDead bool, expireTime int) (*rChannel, error) {
// 	return r.getChannelQueueReset(conn, exChangeName, exChangeType, queueName, route, isDead, expireTime, false)
// }

// todo channel关闭还是连接的状态下删除?
func (r *RabbitPool) deleteChannel(conn *rConn, exChangeName string, exChangeType string, queueName string, route string) {
	channelHashCode := channelHashCode(r.clientType, conn.index, exChangeName, exChangeType, queueName, route)
	r.channelPool[channelHashCode].ch.Close()
	if r.channelPool[channelHashCode].ch.IsClosed() {
		delete(r.channelPool, channelHashCode)
	}
}

/*
todo 需绑定队列
*/
func (r *RabbitPool) getChannelQueueReset(conn *rConn, exChangeName string, exChangeType string, queueName string, route string, isDead bool, expireTime int, isReset bool) (*rChannel, error) {
	channelHashCode := channelHashCode(r.clientType, conn.index, exChangeName, exChangeType, queueName, route)
	if isReset {
		if r.channelPool[channelHashCode].ch.IsClosed() {
			delete(r.channelPool, channelHashCode)
		}
	}
	if channelQueues, ok := r.channelPool[channelHashCode]; ok {
		return channelQueues, nil
	}
	//初始化channel
	rChannel, err := r.initChannels(conn, exChangeName, exChangeType, queueName, route)
	if err != nil {
		return nil, err
	}
	channel, err := rDeclare(conn, r.clientType, rChannel, exChangeName, exChangeType, queueName, route, isDead, "", "", "")
	if err != nil {
		return nil, err
	}
	rChannel.ch = channel.ch
	r.channelPool[channelHashCode] = rChannel
	return rChannel, nil

}

/*
@param host string: rabbitmq主机ip
@param port int:  rabbitmq主机端口
@param user string: 连接用户名
@param password string: 连接密码
@param args string: 可额外设置vhost
*/
func InitPool(amqpconfig *amqpConfig) (*RabbitPool, error) {
	var instancePool *RabbitPool
	var err error

	//初始化生产者
	switch amqpconfig.rabbitType {
	case 1:
		instancePool = NewProductPool()
	case 2:
		instancePool = NewConsumePool()
	default:
		instancePool = nil
		fmt.Printf("get conn pool err: %v \n", amqpconfig.rabbitType)
		err = fmt.Errorf("rabbit type error")
	}
	if instancePool != nil {
		err = instancePool.Connect(amqpconfig)
		if err != nil {
			fmt.Println("errs is ", err)
			instancePool = nil
		} else {
			// 启动 goroutine 监测连接池健康状态
			go monitorPool(instancePool)
		}
	}

	return instancePool, err
}

/*
初始化连接池
*/
func (r *RabbitPool) initConnections(isLock bool) error {
	r.connections[r.clientType] = []*rConn{}
	var i int32 = 0
	for i = 0; i < r.maxConnection; i++ {
		itemConnection, err := rConnect(r, isLock)
		if err != nil {
			return err
		} else {
			r.connections[r.clientType] = append(r.connections[r.clientType], &rConn{conn: itemConnection, index: i})
		}
	}
	return nil
}

/*
初始化信道池
*/
func (r *RabbitPool) initChannels(conn *rConn, exChangeName string, exChangeType string, queueName string, route string) (*rChannel, error) {
	channel, err := rCreateChannel(conn)
	if err != nil {
		return nil, err
	}
	rChannel := &rChannel{ch: channel, index: 0}
	return rChannel, nil
}

/*
原rabbitmq连接
*/
func rConnect(r *RabbitPool, islock bool) (*amqp.Connection, error) {
	return connection(r.user, r.password, r.host, r.port, r.virtualHost)
}

func connection(user string, password string, host string, port int, vHost string) (*amqp.Connection, error) {
	connectionUrl := fmt.Sprintf("amqp://%s:%s@%s:%d%s", user, password, host, port, vHost)
	//fmt.Println(connectionUrl)
	client, err := amqp.Dial(connectionUrl)
	if err != nil {
		client = nil
	}
	return client, err
}

/*
创建rabbitmq信道
*/
func rCreateChannel(conn *rConn) (*amqp.Channel, error) {
	ch, err := conn.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("create Connect Channel Error: %s", err.Error())
	}
	return ch, nil
}

/*
绑定并声明

@param rconn *rConn tcp连接对象

@param clientType int 客户端类型

@param channel 信道

@param exChangeName 交换机名称

@param exChangeType 交换机类型

@param queueName 队列名称

@param route 路由key

@param isDeadQueue 是否是死信队列

@param deadQueueExpireTime int 死信队列到期时间
*/
func rDeclare(rconn *rConn, clientType int, channel *rChannel, exChangeName string, exChangeType string, queueName string, route string, isDeadQueue bool, oldExChangeName string, oldQueueName, oldRoute string) (*rChannel, error) {
	if clientType == RABBITMQ_TYPE_PUBLISH {
		if (len(exChangeType) == 0) || (exChangeType != EXCHANGE_TYPE_DIRECT && exChangeType != EXCHANGE_TYPE_FANOUT && exChangeType != EXCHANGE_TYPE_TOPIC) {
			return channel, errors.New("交换机类型错误")
		}
	}
	newChannel := channel.ch
	err := newChannel.ExchangeDeclare(exChangeName, exChangeType, true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("MQ注册交换机失败:%s", err)
	}
	argsQue := make(map[string]interface{})
	if isDeadQueue {
		argsQue["x-dead-letter-exchange"] = oldExChangeName
		oldRoute = strings.TrimSpace(oldRoute)
		if len(oldRoute) > 0 {
			argsQue["x-dead-letter-routing-key"] = oldRoute
		}
	}
	queue, err := newChannel.QueueDeclare(queueName, true, false, false, false, argsQue)
	if err != nil {
		return nil, fmt.Errorf("MQ注册队列失败:%s", err)
	}
	err = newChannel.QueueBind(queue.Name, route, exChangeName, false, nil)
	if err != nil {
		return nil, fmt.Errorf("MQ绑定队列失败:%s", err)
	}
	// if (clientType != RABBITMQ_TYPE_PUBLISH && exChangeType != EXCHANGE_TYPE_FANOUT) || (clientType == RABBITMQ_TYPE_CONSUME && (exChangeType == EXCHANGE_TYPE_FANOUT || exChangeType == EXCHANGE_TYPE_DIRECT)) {
	// 	argsQue := make(map[string]interface{})
	// 	if isDeadQueue {
	// 		argsQue["x-dead-letter-exchange"] = oldExChangeName
	// 		oldRoute = strings.TrimSpace(oldRoute)
	// 		if len(oldRoute) > 0 {
	// 			argsQue["x-dead-letter-routing-key"] = oldRoute
	// 		}

	// 	}
	// 	queue, err := newChannel.QueueDeclare(queueName, true, false, false, false, argsQue)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("MQ注册队列失败:%s", err)
	// 	}
	// 	err = newChannel.QueueBind(queue.Name, route, exChangeName, false, nil)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("MQ绑定队列失败:%s", err)
	// 	}
	// }
	channel.ch = newChannel
	return channel, nil
}

/*
消费者处理
*/
// func rConsume(pool *RabbitPool) {
// 	for _, v := range pool.consumeReceive {
// 		go func(pool *RabbitPool, receive *ConsumeReceive) {
// 			rListenerConsume(pool, receive)
// 		}(pool, v)
// 	}
// 	/**
// 	创建一个协程监听任务
// 	*/
// 	select {
// 	//case data := <-pool.errorChanel:
// 	case <-pool.errorChanel:
// 		statusLock.Lock()
// 		status = true
// 		statusLock.Unlock()
// 		retryConsume(pool)
// 	}

// }
func rConsume(pool *RabbitPool) {
	// 创建一个退出通道，用于结束协程
	done := make(chan struct{})
	for _, v := range pool.consumeReceive {
		go func(pool *RabbitPool, receive *ConsumeReceive) {
			rListenerConsume(pool, receive)
		}(pool, v)
	}
	// 创建一个协程监听任务
	go func() {
		// 在这里等待从 errorChanel 接收消息
		<-pool.errorChanel
		close(done) // 接收到消息后关闭退出通道
	}()

	// 等待退出通道关闭，同时执行 retryConsume
	<-done
	statusLock.Lock()
	status = true
	statusLock.Unlock()
	retryConsume(pool)
}

/*
重连处理
*/
func retryConsume(pool *RabbitPool) {
	rmqlog(fmt.Sprintf("2秒后开始重试:[%d]", pool.consumeCurrentRetry))
	atomic.AddInt32(&pool.consumeCurrentRetry, 1)
	time.Sleep(time.Second * 2)
	_, err := rConnect(pool, true)
	if err != nil {
		retryConsume(pool)
	} else {
		statusLock.Lock()
		status = false
		statusLock.Unlock()
		_ = pool.initConnections(false)
		rConsume(pool)
	}

}

/*
监听消费
*/
func rListenerConsume(pool *RabbitPool, receive *ConsumeReceive) {
	var i int32 = 0
	for i = 0; i < pool.consumeMaxChannel; i++ {
		itemI := i
		go func(num int32, p *RabbitPool, r *ConsumeReceive) {
			consumeTask(num, p, r)
		}(itemI, pool, receive)
	}
}

var statusLock sync.Mutex
var status bool = false

func setConnectError(pool *RabbitPool, code int, message string) {
	statusLock.Lock()
	defer statusLock.Unlock()

	if !status {
		pool.errorChanel <- &amqp.Error{
			Code:   code,
			Reason: message,
		}
	}
	status = true
}

/*
消费任务
*/

func consumeTask(num int32, pool *RabbitPool, receive *ConsumeReceive) {
	//获取请求连接
	closeFlag := false
	pool.connectionLock.Lock()
	conn := pool.getConnection()
	pool.connectionLock.Unlock()
	//生成处理channel 根据最大channel数处理
	channel, err := rCreateChannel(conn)
	if err != nil {
		if receive.EventFail != nil {
			receive.EventFail(RCODE_CHANNEL_CREATE_ERROR, NewRabbitMqError(RCODE_CHANNEL_CREATE_ERROR, "channel create error", err.Error()), nil)
		}
		return
	}
	defer func() {
		_ = channel.Close()
		_ = conn.conn.Close()
	}()
	//defer
	// notifyClose := make(chan *amqp.Error)
	closeChan := make(chan *amqp.Error, 1)
	rChanels := &rChannel{ch: channel, index: num}
	deadRChanels := &rChannel{ch: channel, index: num}

	deadExchangeName := fmt.Sprintf("%s-%s", receive.ExchangeName, "dead")
	deadQueueName := fmt.Sprintf("%s-%s", receive.QueueName, "dead")
	deadRouteKey := ""
	if len(receive.Route) > 0 {
		deadRouteKey = fmt.Sprintf("%s-%s", receive.Route, "dead")
	}

	//rChanels, err = rDeclare(conn, pool.clientType, rChanels, receive.ExchangeName, receive.ExchangeType, receive.QueueName, receive.Route, receive.IsDead, receive.DeadExchangeName, receive.DeadQueueName, receive.DeadRoute)
	_, err = rDeclare(conn, pool.clientType, rChanels, receive.ExchangeName, receive.ExchangeType, receive.QueueName, receive.Route, false, "", "", "")
	//如果存在死信队列 则需要声明
	if receive.IsTry {

		if num%2 == 0 {

			deadChannel, deadErr := rCreateChannel(conn)
			if deadErr != nil {
				if receive.EventFail != nil {
					receive.EventFail(RCODE_CHANNEL_CREATE_ERROR, NewRabbitMqError(RCODE_CHANNEL_CREATE_ERROR, "dead channel create error", err.Error()), nil)
				}
				return
			}
			defer func() {
				_ = deadChannel.Close()
			}()

			_, err = rDeclare(conn, pool.clientType, deadRChanels, deadExchangeName, EXCHANGE_TYPE_DIRECT, deadQueueName, deadRouteKey, true, receive.ExchangeName, receive.QueueName, receive.Route)
		}
	}
	if err != nil {
		if receive.EventFail != nil {
			receive.EventFail(RCODE_CHANNEL_QUEUE_EXCHANGE_BIND_ERROR, NewRabbitMqError(RCODE_CHANNEL_QUEUE_EXCHANGE_BIND_ERROR, "交换机/队列/绑定失败", err.Error()), nil)
		}
		return
	}
	// 获取消费通道
	//确保rabbitmq会一个一个发消息
	_ = channel.Qos(1, 0, false)
	msgs, err := channel.Consume(
		receive.QueueName, // queue
		"",                // consumer
		false,             // auto-ack
		false,             // exclusive
		false,             // no-local
		false,             // no-wait
		nil,               // args
	)
	if nil != err {
		if receive.EventFail != nil {
			receive.EventFail(RCODE_GET_CHANNEL_ERROR, NewRabbitMqError(RCODE_GET_CHANNEL_ERROR, fmt.Sprintf("获取队列 %s 的消费通道失败", receive.QueueName), err.Error()), nil)
		}
		return
	}

	//一旦消费者的channel有错误，产生一个amqp.Error，channel监听并捕捉到这个错误
	notifyClose := channel.NotifyClose(closeChan)
	for {
		select {
		case data := <-msgs:
			if receive.IsAutoAck { //如果是自动确认,否则需使用回调用 newRetryClient Ack
				_ = data.Ack(true)
			}
			if receive.EventSuccess != nil {
				retryClient := newRetryClient(channel, &data, data.Headers, deadExchangeName, deadQueueName, deadRouteKey, pool, receive)
				var isOk bool
				if pool.sLogger != nil{
					isOk = receive.EventSuccess(data.Body, data.Headers, retryClient,pool.sLogger)
				} else {
					isOk = receive.EventSuccess(data.Body, data.Headers, retryClient,nil)
				}
				if !isOk && receive.IsTry {
					retryNum, ok := data.Headers["retry_nums"]
					var retryNums int32
					if !ok {
						retryNums = 0
					} else {
						retryNums = retryNum.(int32)
					}
					retryNums += 1
					if retryNums >= receive.MaxReTry {
						if receive.EventFail != nil {
							receive.EventFail(RCODE_RETRY_MAX_ERROR, NewRabbitMqError(RCODE_RETRY_MAX_ERROR, "The maximum number of retries exceeded. Procedure", ""), data.Body)
						}
					} else {
						go func(tryNum int32) {
							time.Sleep(time.Millisecond * 200)
							header := make(map[string]interface{}, 1)
							header["retry_nums"] = tryNum

							expirationTime, errs := RandomAround(pool.minRandomRetryTime, pool.maxRandomRetryTime)
							if errs != nil {
								expirationTime = 5000
							}
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							err = channel.PublishWithContext(ctx, deadExchangeName, deadRouteKey, false, false, amqp.Publishing{
								ContentType:  "text/plain",
								Body:         data.Body,
								Expiration:   strconv.FormatInt(expirationTime, 10),
								Headers:      header,
								DeliveryMode: amqp.Persistent,
							})
						}(retryNums)
					}
				}
			}
		//一但有错误直接返回 并关闭信道
		case e := <-notifyClose:
			if receive.EventFail != nil {
				receive.EventFail(RCODE_CONNECTION_ERROR, NewRabbitMqError(RCODE_CONNECTION_ERROR, fmt.Sprintf("消息处理中断: queue:%s\n", receive.QueueName), e.Error()), nil)
			}
			setConnectError(pool, e.Code, fmt.Sprintf("消息处理中断: %s \n", e.Error()))
			closeFlag = true
		}
		if closeFlag {
			break
		}
	}
}

/*
获取生产者连接
*/
func tryConn(pool *RabbitPool, rc *rConn, currentTry int, isRetry bool, exchangeName string, exchangeType string, queueName string, route string) (*rConn, bool) {
	tryStatus := false
	if isRetry {
		tryStatus = true
		rmqlog("连接中断,2秒后开始重试")
		atomic.AddInt32(&pool.productCurrentRetry, 1)
		time.Sleep(time.Second * 2)
	}
	if rc == nil {
		rc = pool.getConnection()
	}
	if rc.conn == nil || rc.conn.IsClosed() {
		rmqlog("开始尝试重试连接")
		var err error
		pool.deleteChannel(rc, exchangeName, exchangeType, queueName, route)
		rc.conn, err = connection(pool.user, pool.password, pool.host, pool.port, pool.virtualHost)
		if err != nil {
			rmqlog("重试连接失败")
			tryConn(pool, rc, currentTry, true, exchangeName, exchangeType, queueName, route)
		}
	}
	return rc, tryStatus
}

/*
发送消息
*/
func rPush(pool *RabbitPool, data *RabbitMqData, sendTime int) *RabbitMqError {
	if sendTime >= pool.pushMaxTime {
		fmt.Println("//todel debug send failed! start write localdata to file...")
		writeToLocalFile(data.Data, data.Localfile)
		return NewRabbitMqError(RCODE_PUSH_MAX_ERROR, "重试超过最大次数", "")
	}
	pool.channelLock.Lock()
	conn := pool.getConnection()
	conn, isTry := tryConn(pool, conn, 0, false, data.ExchangeName, data.ExchangeType, data.QueueName, data.Route)
	rChannels, err := pool.getChannelQueueReset(conn, data.ExchangeName, data.ExchangeType, data.QueueName, data.Route, false, 0, isTry)
	pool.channelLock.Unlock()
	if err != nil {
		//fmt.Println(err)
		return NewRabbitMqError(RCODE_GET_CHANNEL_ERROR, "获取信道失败", err.Error())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = rChannels.ch.PublishWithContext(ctx, data.ExchangeName, data.Route, false, false, amqp.Publishing{
			ContentType:  "text/plain",
			Body:         []byte(data.Data),
			DeliveryMode: amqp.Persistent, //持久化到磁盘
		})
		if err != nil { //如果消息发送失败, 重试发送
			// todo 多次发送失败写入本地磁盘
			//pool.channelLock.Unlock()
			//如果没有发送成功,休息两秒重发
			time.Sleep(time.Second * 2)
			sendTime++
			return rPush(pool, data, sendTime)
		}

	}
	return nil
}

/*
信道hashcode
*/
func channelHashCode(clientType int, connIndex int32, exChangeName string, exChangeType string, queueName string, route string) int64 {
	channelHashCode := hashCode(fmt.Sprintf("%d-%d-%s-%s-%s-%s", clientType, connIndex, exChangeName, exChangeType, queueName, route))
	return channelHashCode
}

/*
计算hashcode唯一值
*/
func hashCode(s string) int64 {
	v := int64(crc32.ChecksumIEEE([]byte(s)))
	if v >= 0 {
		return v
	}
	if -v >= 0 {
		return -v
	}
	return -1
}

/*
随机数

@param int length 生成长度
*/
func RandomNum(length int) string {
	numberAttr := [10]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	numberLen := len(numberAttr)
	rand.New(rand.NewSource(time.Now().UnixNano()))
	var sb strings.Builder
	for i := 0; i < length; i++ {
		itemInt := numberAttr[rand.Intn(numberLen)]
		sb.WriteString(strconv.Itoa(itemInt))
	}
	randStr := sb.String()
	sb.Reset()
	return randStr
}

func RandomAround(min, max int64) (int64, error) {
	if min > max {
		return 0, errors.New("the min is greater than max")
	}
	//rand.Seed(time.Now().UnixNano())
	if min < 0 {
		f64Min := math.Abs(float64(min))
		i64Min := int64(f64Min)
		result, _ := rand2.Int(rand2.Reader, big.NewInt(max+1+i64Min))

		return result.Int64() - i64Min, nil
	} else {
		result, _ := rand2.Int(rand2.Reader, big.NewInt(max-min+1))
		return min + result.Int64(), nil
	}
}

var UTFALL_SECOND = "2006-01-02 15:04:05"
var cstZone = time.FixedZone("CST", 8*3600)

func rmqlog(message string) {
	times := time.Now().In(cstZone).Format(UTFALL_SECOND)
	fmt.Printf("%s %s\n", times, message)
}
