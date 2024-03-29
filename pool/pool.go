package workerpool

import (
	"errors"
	"fmt"
	"sync"
)

const (
	defaultCapacity = 100
	maxCapacity     = 10000
)

var (
	ErrNoIdleWorkerInPool = errors.New("no idle worker in pool")
	ErrWorkerPoolFreed    = errors.New("wokerpool freed")
)

type Task func()

type Pool struct {
	capacity int
	preAlloc bool // 是否在创建pool的时候，就预创建workers，默认值为：false

	// 当pool满的情况下，新的Schedule调用是否阻塞当前goroutine。默认值：true
	// 如果block = false，则Schedule返回ErrNoWorkerAvailInPool
	block  bool
	active chan struct{}  // 有缓冲 channel，用于记录当前活跃的 worker 数量
	tasks  chan Task      // 无缓冲 channel
	wg     sync.WaitGroup // 销毁时等待所有 worker 退出
	quit   chan struct{}  // 通知各个 worker 退出的信号
}

// 接收一个 capacity 参数与多个 Option 选项参数
func New(capacity int, opts ...Option) *Pool {
	if capacity <= 0 { // 防御性校验，当传入参数不合理是主动纠错
		capacity = defaultCapacity
	}
	if capacity > maxCapacity {
		capacity = maxCapacity
	}

	p := &Pool{
		capacity: capacity,
		block:    true,
		tasks:    make(chan Task),
		quit:     make(chan struct{}),
		active:   make(chan struct{}, capacity),
	}
	// 遍历 opts，将每个 Option 选项参数应用到 p 上
	for _, opt := range opts {
		opt(p)
	}
	fmt.Printf("workerpool start(preAlloc=%t)\n", p.preAlloc)
	// 提前创建 goroutine
	if p.preAlloc {
		for i := 0; i < p.capacity; i++ {
			p.newWorker(i + 1)
			p.active <- struct{}{}
		}
	}
	go p.run()
	return p
}

// 监听 pool 创建与退出信号
func (p *Pool) run() {
	idx := len(p.active)

	if !p.preAlloc {
	loop:
		for t := range p.tasks {
			p.returnTask(t)
			select {
			case <-p.quit:
				return
			case p.active <- struct{}{}:
				idx++
				p.newWorker(idx)
			default:
				break loop
			}
		}
	}

	for {
		select {
		case <-p.quit:
			return
		case p.active <- struct{}{}:
			// create a new worker
			idx++
			p.newWorker(idx)
		}
	}
}

func (p *Pool) newWorker(i int) {
	p.wg.Add(1)
	go func() {
		// defer 中需要做：1.捕获 panic 2.active 队列减一 3.pool 的 WaitGroup 置为 Done
		defer func() {
			if err := recover(); err != nil {
				fmt.Printf("worker[%03d]: recover panic[%s] and exit\n", i, err)
				<-p.active
			}
			p.wg.Done()
		}()
		fmt.Printf("worker[%03d]: start\n", i)
		for {
			select {
			case <-p.quit: // 监听 quit
				fmt.Printf("worker[%03d]: exit\n", i)
				<-p.active
				return
			case t := <-p.tasks:
				fmt.Printf("worker[%03d]: receive a task\n", i)
				t()
			}
		}
	}()
}

func (p *Pool) Schedule(t Task) error {
	select {
	case <-p.quit:
		return ErrWorkerPoolFreed
	case p.tasks <- t:
		return nil
	default:
		if p.block {
			p.tasks <- t
			return nil
		}
		return ErrNoIdleWorkerInPool
	}
}

// 发送 quit 信号，等待所有 worker 完成任务退出
func (p *Pool) Free() {
	close(p.quit)
	p.wg.Wait()
	fmt.Printf("workerpool freed\n")
}

// 防止 task 阻塞，使用 goroutine 异步发送 task
func (p *Pool) returnTask(t Task) {
	go func() {
		p.tasks <- t
	}()
}
