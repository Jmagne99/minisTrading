package minis

import (
	"fmt"
	"sync"

	"github.com/deltafund/api-fix/marketdata"
	"github.com/deltafund/api-fix/order"
	"github.com/deltafund/api-fix/security"
	"github.com/deltafund/api-fix/utils"
	"github.com/deltafund/components-support/broker"
	"github.com/deltafund/components-support/position"
	"github.com/deltafund/components-support/settings"
	"github.com/deltafund/components-support/storage"
)

type Balancer struct {
	rwMutex  sync.RWMutex
	security security.Security

	//position     position.Position
	side    order.Side
	account string
	broker  broker.Broker
	logger  *storage.Logger

	activeOrder *order.Order
	sentOrder   *order.Order

	px               float64
	qty              float64
	mktPx            float64
	combinedPosition position.Position
	pendingCancel    bool
	cancelRejected   bool
	cumQty           float64
	avgBuyPx         float64
	avgSellPx        float64
	settingsManager  *settings.SettingsManager
	//Switchs
	enabledAll bool
	enabled    bool
}

func NewBalancer(securityFuture security.Security,
	miniSecurity security.Security, account string, broker broker.Broker) *Balancer {

	loggerName := "balancer"
	//if side == order.Side_SELL {
	//	loggerName = "balancer-sell"
	//}

	return &Balancer{
		logger:    storage.NewLogger(loggerName),
		security:  securityFuture,
		side:      order.Side_BUY,
		account:   account,
		broker:    broker,
		px:        0.0,
		qty:       0.0,
		mktPx:     0.0,
		cumQty:    0.0,
		avgBuyPx:  0.0,
		avgSellPx: 0.0,
		//newMiniHistoricalPos: 0.0,
		//newStdHistoricalPos:  0.0,
		pendingCancel:  false,
		cancelRejected: false,
		enabledAll:     true, //en produccion inicializar en false
		enabled:        true, //en produccion inicializar en false
	}
}

func (b *Balancer) OnOrderPlaced(orderPlaced order.OrderPlaced) {
	//b.logger.Printf("OnOrderPlaced: %+v", orderPlaced)

	b.rwMutex.Lock()
	if b.invalidEvent(orderPlaced.OrderEvent) {
		b.rwMutex.Unlock()
		return
	}

	b.sentOrder = nil
	b.activeOrder = &orderPlaced.Order
	b.rebalance()
	b.rwMutex.Unlock()
}

func (b *Balancer) OnOrderPlaceRejected(orderPlaceRejected order.OrderPlaceRejected) {
	b.logger.Printf("OnOrderPlaceRejected: %+v", orderPlaceRejected)
	b.rwMutex.Lock()
	if b.invalidEvent(orderPlaceRejected.OrderEvent) {
		b.rwMutex.Unlock()
		return
	}
	b.pendingCancel = true
	b.rebalance()
	b.rwMutex.Unlock()
}

func (b *Balancer) BeforeOrderPlacement(beforeOrderPlacement order.BeforeOrderPlacement) {
	b.logger.Printf("BeforeOrderPlacement: %+v", beforeOrderPlacement)
}

func (b *Balancer) OnOrderReplaced(orderReplaced order.OrderReplaced) {
	b.logger.Printf("OnOrderReplaced: %+v", orderReplaced)
	b.rwMutex.Lock()
	b.sentOrder = nil
	b.activeOrder = orderReplaced.NewOrder
	b.rebalance()
	b.rwMutex.Unlock()
}
func (b *Balancer) OnOrderReplaceRejected(orderReplaceRejected order.OrderReplaceRejected) {
	b.logger.Printf("OnOrderReplaceRejected: %+v", orderReplaceRejected)
	b.rwMutex.Lock()
	if b.invalidEvent(orderReplaceRejected.OrderEvent) {
		b.rwMutex.Unlock()
		return
	}
	b.pendingCancel = true
	b.rebalance()
	b.rwMutex.Unlock()
}
func (b *Balancer) BeforeOrderReplacement(beforeOrderReplacement order.BeforeOrderReplacement) {
}

func (b *Balancer) OnOrderCancelled(orderCancelled order.OrderCancelled) {
	b.logger.Printf("OnOrderCancelled: %+v", orderCancelled)
	b.rwMutex.Lock()
	if b.invalidEvent(orderCancelled.OrderEvent) {
		b.rwMutex.Unlock()
		return
	}
	b.sentOrder = nil
	b.activeOrder = nil
	b.rwMutex.Unlock()
}

func (b *Balancer) OnOrderCancelRejected(orderCancelRejected order.OrderCancelRejected) {
	b.logger.Printf("OnOrderCancelRejected: %+v", orderCancelRejected)
	b.rwMutex.Lock()
	if b.invalidEvent(orderCancelRejected.OrderEvent) {
		b.rwMutex.Unlock()
		return
	}
	b.pendingCancel = true
	b.cancelRejected = true
	b.rebalance()
	b.rwMutex.Unlock()
}
func (b *Balancer) BeforeOrderCancellation(beforeOrderCancellation order.BeforeOrderCancellation) {
}

func (b *Balancer) placeOrder() {
	request := order.PlaceOrderRequest{
		OrderId:  utils.UUID(),
		Account:  b.account,
		Security: b.security,
		Qty:      b.qty,
		Px:       b.px,
		Side:     b.side,
		Type:     order.Type_LIMIT,
		Validity: order.Validity_DAY,
	}
	b.px = 0.0
	newOrder, err := b.broker.PlaceOrder(request, b)
	if err != nil {
		b.logger.Printf("Cannot place new order: %+v. Error: %v", request, err)
		return
	}

	b.sentOrder = newOrder
}

func (b *Balancer) replaceOrder() {
	request := order.ReplaceOrderRequest{
		Order: *b.activeOrder,
		Qty:   b.qty,
		Px:    b.px,
	}

	err := b.broker.ReplaceOrder(request)
	if err != nil {
		b.logger.Printf("Cannot replace order %+v with request: %+v. Error: %s", b.activeOrder, request, err)
		return
	}

	b.sentOrder = b.activeOrder
	b.sentOrder.Px = b.px
	b.sentOrder.Qty = 1
}

func (b *Balancer) rebalance() {
	//b.logger.Printf("Rebalance b.Px=%+v,  qty=%+v", b.px, b.qty)
	if b.pendingCancel {
		b.logger.Printf("Rebalance pendingCancel true")
		//b.removeOrder()
	} else if !b.enabledAll {
		b.logger.Printf("Cannot rebalance. Robot is disabled for all assets.")
		b.removeOrder()
	} else if b.px <= 0.0 || b.qty == 0.0 {
		b.logger.Printf("Cannot rebalance. Px or qty <= 0  px : %+v qty : %+v", b.px, b.qty)
		//b.removeOrder()
	} else if b.sentOrder != nil {
		b.logger.Printf("Cannot rebalance. Another action is pending acknowledgement")
	} else if b.activeOrder == nil && b.sentOrder == nil {
		fmt.Println("Rebalance sending order")
		b.placeOrder()
	} else if b.activeOrder.Px != b.px || b.activeOrder.Qty != b.qty {
		b.placeOrder()
		b.logger.Printf("active order: %+v\n b Px: %+v\n b qty: %v\n", b.activeOrder, b.px, b.qty)
	} else if b.combinedPosition.NetQty >= 60 || b.combinedPosition.NetQty <= -60 {
		b.placeOrder()
	} else {
		fmt.Printf("%+vcannot Rebalance, security is a mini %+v :", b.security.Symbol, b.security.Harbour)
	}
}

func (b *Balancer) cancelOrder() {

	request := order.CancelOrderRequest{
		Order: *b.activeOrder,
	}
	err := b.broker.CancelOrder(request)
	if err != nil {
		b.logger.Printf("Cannot cancel order %+v. Error %v", b.activeOrder, err)
	}
	b.sentOrder = b.activeOrder
}

func (b *Balancer) invalidEvent(event order.OrderEvent) bool {
	invalidOrder := (b.activeOrder == nil && b.sentOrder == nil)
	invalidOrder = invalidOrder ||
		((b.activeOrder != nil && b.activeOrder.Id != event.Order.Id) && (b.sentOrder != nil && b.sentOrder.Id != event.Order.Id))
	if invalidOrder {
		b.logger.Printf("Received order event for an unknown order: %+v", event)
	}
	return invalidOrder
}

func (b *Balancer) removeOrder() {
	if b.activeOrder != nil && b.security.Symbol != "MIN" {
		if b.sentOrder != nil {
			b.pendingCancel = true
		} else if !b.cancelRejected {
			b.cancelOrder()
		}
	}
}

func (b *Balancer) OnOrderFilled(orderFilled order.OrderFilled) {
	b.logger.Printf("OnOrderFilled: %+v", orderFilled)

	if orderFilled.Order.Security.Harbour == "MIN" {

		b.rwMutex.Lock()
		b.sentOrder = nil
		b.activeOrder = nil
		b.px = orderFilled.Px
		b.calculateQty()
		b.rebalance()
		b.rwMutex.Unlock()

	}
}

func (b *Balancer) OnOrderPartiallyFilled(orderPartiallyFilled order.OrderPartiallyFilled) {
	//chequeo que instrumento es, si es mini, guardo los precios
	b.logger.Printf("\nBALANCER orderPartiallyFilled: %+v", orderPartiallyFilled)
	if orderPartiallyFilled.Order.Security.Harbour == "MIN" {
		b.rwMutex.Lock()
		b.px = orderPartiallyFilled.Px
		b.calculateQty()
		b.rebalance()
		b.rwMutex.Unlock()

	}
}

func (b *Balancer) OnOrderRegistered(orderRegistered order.OrderRegistered) {}
func (b *Balancer) OnTradeCancel(tradeCancel order.TradeCancel)             {}
func (b *Balancer) OnStartFinish(exchange security.Exchange)                {}
func (b *Balancer) OnTradeFromAnotherAccount(tradeFromAnotherAccount order.TradeFromAnotherAccount) {
}

func (b *Balancer) OnSecurityPositionChange(security security.Security, event position.PositionEvent) {
	b.logger.Printf("\nOnSecurityPositionChange event: %+v security: %+v ", event, security)
	b.avgBuyPx = event.NewPosition.AvgBuyPx
	b.avgSellPx = event.NewPosition.AvgSellPx
}

func (b *Balancer) OnSyntheticPositionChange(syntheticInstrument string, event position.PositionEvent) {
	b.logger.Printf("Synthetic Position, newPosition %s: %+v\n", syntheticInstrument, event.NewPosition)
	b.rwMutex.Lock()
	b.combinedPosition = event.NewPosition

	//if strings.Contains(syntheticInstrument, "ROS") {
	//	b.avgBuyPx = event.NewPosition.AvgBuyPx
	//	b.avgSellPx = event.NewPosition.AvgSellPx
	//}

	b.calculateQty()
	b.rebalance()
	b.rwMutex.Unlock()
}

func (b *Balancer) calculateQty() {
	b.qty = 0
	if b.combinedPosition.NetQty >= 60 {
		b.side = order.Side_SELL
		b.qty = 1

	} else if b.combinedPosition.NetQty <= -60 {
		b.side = order.Side_BUY
		b.qty = 1
	}
	b.logger.Printf("calculateQty %+v\n", b.combinedPosition.NetQty)

}

// func (b *Balancer) OnBookUpdated(bookUpdated marketdata.BookUpdated) {
// }

func (b *Balancer) OnDisconnect(exchange security.Exchange) {}
func (b *Balancer) OnSecurityStatus(securityStatus marketdata.SecurityStatus) {
}

/// settings callbacks

func (b *Balancer) OnBotSettingChange(botSetting settings.BotSetting) {}
func (b *Balancer) OnBotEnabledChange(botEnabled settings.Enabled) {
	b.logger.Printf("%v Balancer OnBotEnabledChange %+v\n", b.security.Symbol, botEnabled)
	b.rwMutex.Lock()
	if b.enabledAll {
		if !botEnabled.Value {
			b.enabledAll = false
		}
	} else {
		if botEnabled.Value {
			b.logger.Printf("Activating. Balancer Bot is enabled")
			b.enabledAll = true
		}
	}
	b.rebalance()
	b.rwMutex.Unlock()
}

func (b *Balancer) OnCommand(command settings.FrontCommand) {}
func (b *Balancer) OnAssetSettingChange(assetSetting settings.AssetSetting) {
	if assetSetting.Asset != b.security.Symbol {
		b.logger.Printf("Ignoring asset event %+v: %+v", &b.security.Symbol, assetSetting)
		return
	}
	rebalance := false
	switch assetSetting.Key {
	case settings.SWITCH_ASSET_BID:
		b.logger.Printf("%+v Switch asset bid: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_BUY {
			b.switchState(assetSetting)
		}

	case settings.SWITCH_ASSET_ASK:
		b.logger.Printf("%+v Switch asset ask: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_SELL {
			b.switchState(assetSetting)
		}

	case settings.CHANGE_VOL_BID:
		b.logger.Printf("%+v Change vol bid: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_BUY {
			if assetSetting.Value >= 0.0 {
				rebalance = true
				b.rwMutex.Unlock()
			}
		}

	case settings.CHANGE_VOL_ASK:
		b.logger.Printf("%+v Change vol ask: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_SELL {
			if assetSetting.Value >= 0.0 {
				rebalance = true
			}
		}

	case settings.CHANGE_QTY_BID:
		b.logger.Printf("%+v Change qty bid: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_BUY {
			if assetSetting.Value >= 0.0 {
				b.rwMutex.Lock()
				b.qty = assetSetting.Value
				rebalance = true
				b.rwMutex.Unlock()
			}
		}

	case settings.CHANGE_QTY_ASK:
		b.logger.Printf("%+v Change qty ask: %+v", &b.security.Symbol, assetSetting)
		if b.side == order.Side_SELL {
			if assetSetting.Value >= 0.0 {
				b.rwMutex.Lock()
				b.qty = assetSetting.Value
				rebalance = true
				b.rwMutex.Unlock()
			}
		}
	default:
		b.logger.Printf("%+v Asset setting key not recognized: %+v", &b.security.Symbol, assetSetting)
	}

	if rebalance {
		b.rwMutex.Lock()
		//b.px = b.calculatePx()
		b.rebalance()
		b.rwMutex.Unlock()
	} else {
		//mm.logger.Printf("Ignoring rebalance asset settings event: %+v", botSetting)
	}

}

func (b *Balancer) deactivate(notify string) {
	if b.enabled {
		switch notify {
		case "asset":
			b.enabled = false
			if b.side == order.Side_SELL {
				b.settingsManager.ChangeAssetState(settings.SWITCH_ASSET_ASK, 0, b.security.Symbol)
			} else {
				b.settingsManager.ChangeAssetState(settings.SWITCH_ASSET_BID, 0, b.security.Symbol)
			}

		case "global":
			b.enabledAll = false
			b.settingsManager.ChangeRobotState(0)
		default:
			b.logger.Printf(notify)
		}
	}
	b.removeOrder()
}

func (b *Balancer) switchState(assetSetting settings.AssetSetting) {
	if b.enabled {
		if assetSetting.Value == 0 {
			b.rwMutex.Lock()
			b.deactivate("asset")
			b.enabled = false
			b.rwMutex.Unlock()
		}
	} else {
		if assetSetting.Value == 1 {
			b.rwMutex.Lock()
			b.logger.Printf("Activating market maker %v %v\n", b.security.Symbol, b.side)
			b.enabled = true
			b.rwMutex.Unlock()
		}
	}
}

//func (b *Balancer) calculatePx() float64 {
//return b.mktPx

//}
