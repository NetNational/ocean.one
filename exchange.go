package main

import (
	"context"
	"log"
	"time"

	"github.com/MixinMessenger/bot-api-go-client"
	"github.com/MixinMessenger/go-number"
	"github.com/MixinMessenger/ocean.one/config"
	"github.com/MixinMessenger/ocean.one/engine"
	"github.com/MixinMessenger/ocean.one/persistence"
	"github.com/ugorji/go/codec"
)

const EnginePrecision = 8

type Exchange struct {
	books     map[string]*engine.Book
	codec     codec.Handle
	snapshots map[string]bool
}

func NewExchange() *Exchange {
	return &Exchange{
		codec:     new(codec.MsgpackHandle),
		books:     make(map[string]*engine.Book),
		snapshots: make(map[string]bool),
	}
}

func (ex *Exchange) Run(ctx context.Context) {
	go ex.PollMixinMessages(ctx)
	go ex.PollMixinNetwork(ctx)
	ex.PollOrderActions(ctx)
}

func (ex *Exchange) PollOrderActions(ctx context.Context) {
	offset, limit := time.Time{}, 500
	for {
		actions, err := persistence.ListPendingActions(ctx, offset, limit)
		if err != nil {
			log.Println("ListPendingActions", err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, a := range actions {
			log.Println(a)
			offset = a.CreatedAt
		}
		if len(actions) < limit {
			time.Sleep(1 * time.Second)
		}
	}
}

func (ex *Exchange) processOrderAction(ctx context.Context, action *persistence.Action) {
	order := action.Order
	market := order.BaseAssetId + "-" + order.QuoteAssetId
	book := ex.books[market]
	if book == nil {
		book = engine.NewBook(func(taker, maker *engine.Order, amount number.Decimal) {
			for {
				err := persistence.Transact(ctx, taker, maker, amount, EnginePrecision)
				if err == nil {
					break
				}
				log.Println("Engine Transact CALLBACK", err)
				time.Sleep(1 * time.Second)
			}
		}, func(order *engine.Order) {
			for {
				err := persistence.CancelOrder(ctx, order, EnginePrecision)
				if err == nil {
					break
				}
				log.Println("Engine Cancel CALLBACK", err)
				time.Sleep(1 * time.Second)
			}
		})
		go book.Run(ctx)
		ex.books[market] = book
	}
	precision := number.New(1, -EnginePrecision)
	price := number.FromString(order.Price).Mul(precision).Floor().Float64()
	filledPrice := number.FromString(order.FilledPrice).Mul(precision).Floor().Float64()
	remainingAmount := number.FromString(order.RemainingAmount)
	filledAmount := number.FromString(order.FilledAmount)
	book.AttachOrderEvent(ctx, &engine.Order{
		Id:              order.OrderId,
		Side:            order.Side,
		Type:            order.OrderType,
		Price:           uint64(price),
		FilledPrice:     uint64(filledPrice),
		RemainingAmount: remainingAmount,
		FilledAmount:    filledAmount,
		Quote:           order.QuoteAssetId,
		Base:            order.BaseAssetId,
		UserId:          order.UserId,
	}, action.Action)
}

func (ex *Exchange) PollMixinNetwork(ctx context.Context) {
	checkpoint, limit := time.Now().UTC(), 500
	for {
		timestamp, err := persistence.ReadActionCheckpoint(ctx)
		if err != nil {
			log.Println("ReadActionCheckpoint", err)
			time.Sleep(1 * time.Second)
			continue
		}
		checkpoint = timestamp.UTC()
		break
	}

	for {
		snapshots, err := ex.requestMixinNetwork(ctx, checkpoint, limit)
		if err != nil {
			log.Println("PollMixinNetwork ERROR", err)
			time.Sleep(1 * time.Second)
			continue
		}
		for _, s := range snapshots {
			if ex.snapshots[s.SnapshotId] {
				continue
			}
			log.Println(s)
			err := ex.processSnapshot(ctx, s)
			if err != nil {
				log.Println("PollMixinNetwork processSnapshot ERROR", err)
				break
			}
			checkpoint = s.CreatedAt
			ex.snapshots[s.SnapshotId] = true
		}
		if len(snapshots) < limit {
			time.Sleep(1 * time.Second)
		}
	}
}

func (ex *Exchange) PollMixinMessages(ctx context.Context) {
	bot.Loop(ctx, ex, config.ClientId, config.SessionId, config.SessionKey)
}

func (ex *Exchange) OnMessage(ctx context.Context, mc *bot.MessageContext, msg bot.MessageView, userId string) error {
	log.Println(msg, userId)
	return nil
}
