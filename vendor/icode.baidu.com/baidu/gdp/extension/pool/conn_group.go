// Copyright(C) 2021 Baidu Inc. All Rights Reserved.
// Author: Wei Du (duwei04@baidu.com)
// Date: 2021/3/29

package pool

import (
	"context"
	"net"
)

// GroupNewConnFunc 给 Group 创建新的 pool
type GroupNewConnFunc func(addr net.Addr) NewConnFunc

func (gn GroupNewConnFunc) trans() GroupNewElementFunc {
	return func(key interface{}) NewElementFunc {
		return func(ctx context.Context, pool NewElementNeed) (Element, error) {
			conn, err := gn(key.(net.Addr))(ctx)
			if err != nil {
				return nil, err
			}
			return newPConn(conn, pool), nil
		}
	}
}

// NewConnPoolGroup 创建新的 Group
func NewConnPoolGroup(opt *Option, gn GroupNewConnFunc) ConnPoolGroup {
	return &connGroup{
		raw: NewSimplePoolGroup(opt, gn.trans()),
	}
}

// ConnPoolGroup 按照 key 分组的 连接池
type ConnPoolGroup interface {
	Get(ctx context.Context, addr net.Addr) (net.Conn, error)
	GroupStats() GroupStats
	Close() error
	Option() Option
	Range(func(el net.Conn) error) error
}

var _ ConnPoolGroup = (*connGroup)(nil)

type connGroup struct {
	raw SimplePoolGroup
}

func (cg *connGroup) Range(fn func(el net.Conn) error) error {
	return cg.raw.Range(func(el Element) error {
		return fn(el.(net.Conn))
	})
}

func (cg *connGroup) Option() Option {
	return cg.raw.Option()
}

func (cg *connGroup) Get(ctx context.Context, addr net.Addr) (net.Conn, error) {
	el, err := cg.raw.Get(ctx, addr)
	if err != nil {
		return nil, err
	}
	return el.(net.Conn), err
}

func (cg *connGroup) GroupStats() GroupStats {
	return cg.raw.GroupStats()
}

func (cg *connGroup) Close() error {
	return cg.raw.Close()
}
