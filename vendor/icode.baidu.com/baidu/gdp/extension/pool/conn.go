// Copyright(C) 2021 Baidu Inc. All Rights Reserved.
// Author: Wei Du (duwei04@baidu.com)
// Date: 2021/3/29

package pool

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// NewConnFunc 创建新连接
type NewConnFunc func(ctx context.Context) (net.Conn, error)

// Trans 转换为原始的 NewElementFunc
func (nf NewConnFunc) Trans(p *connPool) NewElementFunc {
	return func(ctx context.Context, pool NewElementNeed) (Element, error) {
		raw, err := nf(ctx)
		if err != nil {
			return nil, err
		}
		vc := newPConn(raw, p)
		return vc, nil
	}
}

// NewConnPool 创建新的 net.Conn 的连接池
func NewConnPool(option *Option, newFunc NewConnFunc) ConnPool {
	p := &connPool{}
	p.raw = NewSimplePool(option, newFunc.Trans(p))
	return p
}

// ConnPool 网络连接池
type ConnPool interface {
	Get(ctx context.Context) (net.Conn, error)
	Option() Option
	Stats() Stats
	Range(func(net.Conn) error) error
	Close() error
}

var _ ConnPool = (*connPool)(nil)

// connPool 网络连接池
type connPool struct {
	raw SimplePool
}

// Get get
func (cp *connPool) Get(ctx context.Context) (el net.Conn, err error) {
	value, err := cp.raw.Get(ctx)
	if err != nil {
		return nil, err
	}
	return value.(net.Conn), nil
}

// Put put to pool
func (cp *connPool) Put(value interface{}) error {
	return cp.raw.(NewElementNeed).Put(value)
}

func (cp *connPool) Range(fn func(net.Conn) error) error {
	return cp.raw.Range(func(el Element) error {
		return fn(el.(net.Conn))
	})
}

// Close close pool
func (cp *connPool) Close() error {
	return cp.raw.Close()
}

// Option get pool option
func (cp *connPool) Option() Option {
	return cp.raw.Option()
}

// Stats get pool stats
func (cp *connPool) Stats() Stats {
	return cp.raw.Stats()
}

const (
	statInit = iota
	statStart
	statDone
)

func newPConn(raw net.Conn, p NewElementNeed) *pConn {
	return &pConn{
		raw:      raw,
		pool:     p,
		MetaInfo: NewMetaInfo(),
	}
}

var _ net.Conn = (*pConn)(nil)
var _ Element = (*pConn)(nil)

type pConn struct {
	*MetaInfo

	pool NewElementNeed

	raw net.Conn

	mu      sync.RWMutex
	lastErr error

	readStat  uint8
	writeStat uint8
}

func (c *pConn) setErr(err error) {
	if err != nil {
		c.mu.Lock()
		c.lastErr = err
		c.mu.Unlock()
	}
}

func (c *pConn) Read(b []byte) (n int, err error) {
	c.withLock(func() {
		c.readStat = statStart
	})
	n, err = c.raw.Read(b)
	c.setErr(err)
	c.withLock(func() {
		c.readStat = statDone
	})
	return n, err
}

func (c *pConn) Write(b []byte) (n int, err error) {
	c.withLock(func() {
		c.writeStat = statStart
	})
	n, err = c.raw.Write(b)
	c.setErr(err)
	c.withLock(func() {
		c.writeStat = statDone
	})
	return n, err
}

func (c *pConn) PEReset() {
	// 重置 DeadLine 否则可能在连接池里就会被判断失效了
	// 和 conncheck 有关系
	_ = c.SetDeadline(time.Time{})
	_ = c.SetReadDeadline(time.Time{})
	_ = c.SetWriteDeadline(time.Time{})

	c.withLock(func() {
		c.readStat = statInit
		c.writeStat = statInit
	})
}

var errCloseInRW = errors.New("pConn was closed,but Read or Write operations are still in progress")

func (c *pConn) Close() error {
	c.withLock(func() {
		if c.lastErr == nil && c.isDoing() {
			c.lastErr = errCloseInRW
		}
	})
	return c.pool.Put(c)
}

func (c *pConn) LocalAddr() net.Addr {
	return c.raw.LocalAddr()
}

func (c *pConn) RemoteAddr() net.Addr {
	return c.raw.RemoteAddr()
}

func (c *pConn) SetDeadline(t time.Time) error {
	err := c.raw.SetDeadline(t)
	c.setErr(err)
	return err
}

func (c *pConn) SetReadDeadline(t time.Time) error {
	err := c.raw.SetReadDeadline(t)
	c.setErr(err)
	return err
}

func (c *pConn) SetWriteDeadline(t time.Time) error {
	err := c.raw.SetWriteDeadline(t)
	c.setErr(err)
	return err
}

func (c *pConn) withLock(fn func()) {
	c.mu.Lock()
	fn()
	c.mu.Unlock()
}

func (c *pConn) isDoing() bool {
	return c.readStat == statStart || c.writeStat == statStart
}

// Raw 返回传入的原始的连接
// 需要注意的是，这个也不一定是最底层的 net.Conn，也可能是一个自定义的 net.Conn
// 若需要最底层的 net.Conn,可以使用下面 getRawConn 的方法获取
func (c *pConn) Raw() net.Conn {
	return c.raw
}

func (c *pConn) PERawClose() error {
	return c.raw.Close()
}

func (c *pConn) PEActive() error {
	c.mu.RLock()

	if c.lastErr != nil || c.isDoing() {
		c.mu.RUnlock()
		return ErrBadValue
	}

	c.mu.RUnlock()

	if ea := c.MetaInfo.Active(c.pool.Option()); ea != nil {
		return ea
	}

	if ra, ok := c.raw.(PEActiver); ok {
		if ea := ra.PEActive(); ea != nil {
			return ea
		}
	}

	// 检查底层连接是否有效
	if err := connCheck(c.getRawConn()); err != nil {
		return err
	}

	return nil
}

// getRawConn 返回最底层的 net.Conn
func (c *pConn) getRawConn() net.Conn {
	if cr, ok := c.raw.(interface{ Raw() net.Conn }); ok {
		return cr.Raw()
	}
	return c.raw
}
