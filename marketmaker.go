package minis

import (
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

type MinisMarketMaker struct {
	//Campos que vamos a necesitar para nuestra estrategia
	stdSecurity  security.Security
	miniSecurity security.Security
	position     position.Position
	side         order.Side
	account      string
	broker       broker.Broker
	logger       *storage.Logger

	activeOrder *order.Order
	sentOrder   *order.Order

	px         float64
	qty        float64
	qtyDefault float64
	//netQty          float64
	automaticSpread float64
	avgBuyPx        float64
	avgSellPx       float64
	//Switchs
	enabledAll             bool
	automaticSpreadEnabled bool
	enabled                bool
	unbalanced             bool

	mktPx           float64
	rwMutex         sync.RWMutex
	settingsManager *settings.SettingsManager

	pendingCancel  bool
	cancelRejected bool
}

func NewMinisMarketMaker(securityFuture security.Security,
	side order.Side, account string, broker broker.Broker) *MinisMarketMaker {

	loggerName := "market-maker-buy"
	if side == order.Side_SELL {
		loggerName = "market-maker-sell"
	}

	return &MinisMarketMaker{
		logger:       storage.NewLogger(loggerName),
		miniSecurity: securityFuture,
		side:         side,
		account:      account,
		broker:       broker,
		px:           0.0,
		qty:          0.0,
		mktPx:        0.0,
		qtyDefault:   10.0,
		//netQty:                 0.0,
		automaticSpread:        0.0,
		avgBuyPx:               0.0,
		avgSellPx:              0.0,
		pendingCancel:          false,
		cancelRejected:         false,
		unbalanced:             false,
		enabledAll:             true, //en produccion inicializar en false
		enabled:                true, //en produccion inicializar en false
		automaticSpreadEnabled: true, //en produccion inicializar en false funcion OnAssetSettingChange
	}
}

///////////////// Order Callbacks ////////////////////////////////
func (mm *MinisMarketMaker) OnOrderPlaced(orderPlaced order.OrderPlaced) {
	mm.logger.Printf("OnOrderPlaced: %+v", orderPlaced)

	mm.rwMutex.Lock()
	if mm.invalidEvent(orderPlaced.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}

	mm.sentOrder = nil
	mm.activeOrder = &orderPlaced.Order
	mm.px = mm.calculatePx()
	mm.qty = mm.calculateQty()
	mm.rebalance()
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnOrderPlaceRejected(orderPlaceRejected order.OrderPlaceRejected) {
	mm.logger.Printf("OnOrderPlaceRejected: %+v", orderPlaceRejected)
	mm.rwMutex.Lock()
	if mm.invalidEvent(orderPlaceRejected.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}
	mm.pendingCancel = true
	mm.sentOrder = nil
	mm.rebalance()
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) BeforeOrderPlacement(beforeOrderPlacement order.BeforeOrderPlacement) {
	mm.logger.Printf("BeforeOrderPlacement: %+v", beforeOrderPlacement)
}

func (mm *MinisMarketMaker) OnOrderReplaced(orderReplaced order.OrderReplaced) {
	mm.logger.Printf("OnOrderReplaced: %+v", orderReplaced)
	mm.rwMutex.Lock()
	if mm.invalidEvent(orderReplaced.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}
	mm.sentOrder = nil
	mm.activeOrder = orderReplaced.NewOrder
	mm.rebalance()
	mm.rwMutex.Unlock()
}
func (mm *MinisMarketMaker) OnOrderReplaceRejected(orderReplaceRejected order.OrderReplaceRejected) {
	mm.logger.Printf("OnOrderReplaceRejected: %+v", orderReplaceRejected)
	mm.rwMutex.Lock()
	if mm.invalidEvent(orderReplaceRejected.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}
	mm.pendingCancel = true
	mm.rebalance()
	mm.rwMutex.Unlock()
}
func (mm *MinisMarketMaker) BeforeOrderReplacement(beforeOrderReplacement order.BeforeOrderReplacement) {
}

func (mm *MinisMarketMaker) OnSecurityPositionChange(security security.Security, event position.PositionEvent) {
	//mm.logger.Printf("\nOnSecurityPositionChange event: %+v security: %+v ", event, security)
	mm.logger.Printf("OnSecurityPositionChange Security: \n%+v ", security)

	mm.rwMutex.Lock()
	if mm.miniSecurity.String() == security.String() {
		//mm.netQty = event.NewHistoricalPosition.NetQty
		//mm.avgBuyPx = event.NewPosition.AvgBuyPx
		//mm.avgSellPx = event.NewPosition.AvgSellPx

	}
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnOrderCancelled(orderCancelled order.OrderCancelled) {
	mm.logger.Printf("OnOrderCancelled: %+v", orderCancelled)
	mm.rwMutex.Lock()
	if mm.invalidEvent(orderCancelled.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}
	mm.sentOrder = nil
	mm.activeOrder = nil
	mm.rwMutex.Unlock()
}
func (mm *MinisMarketMaker) OnOrderCancelRejected(orderCancelRejected order.OrderCancelRejected) {
	mm.logger.Printf("OnOrderCancelRejected: %+v", orderCancelRejected)
	mm.rwMutex.Lock()
	if mm.invalidEvent(orderCancelRejected.OrderEvent) {
		mm.rwMutex.Unlock()
		return
	}
	mm.pendingCancel = true
	mm.cancelRejected = true
	mm.rebalance()
	mm.rwMutex.Unlock()
}
func (mm *MinisMarketMaker) BeforeOrderCancellation(beforeOrderCancellation order.BeforeOrderCancellation) {
}

func (mm *MinisMarketMaker) OnOrderFilled(orderFilled order.OrderFilled) {
	mm.logger.Printf("OnOrderFilled: %+v", orderFilled)
	mm.rwMutex.Lock()
	mm.sentOrder = nil
	mm.activeOrder = nil
	mm.qty = mm.calculateQty()
	mm.px = mm.calculatePx()
	mm.rebalance()
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnOrderPartiallyFilled(orderPartiallyFilled order.OrderPartiallyFilled) {
	//mm.logger.Printf("orderPartiallyFilled: %+v", orderPartiallyFilled)
	if mm.sentOrder != nil {
		if mm.sentOrder.Qty == orderPartiallyFilled.Order.Qty && mm.sentOrder.Px == orderPartiallyFilled.Order.Px {
			mm.sentOrder = nil
			//mm.replaceOrder()
		}
	}
	mm.rwMutex.Lock()
	//mm.qty = mm.calculateQty()
	mm.qty = mm.calculateQty()
	mm.px = mm.calculatePx()
	mm.activeOrder = &orderPartiallyFilled.Order
	mm.logger.Printf("PartiallyFilled : mm.qty : %+v cumQty : %+v", mm.qty, orderPartiallyFilled.Order.CumQty)
	mm.rebalance() //o replaceOrder
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnOrderRegistered(orderRegistered order.OrderRegistered) {}
func (mm *MinisMarketMaker) OnTradeCancel(tradeCancel order.TradeCancel)             {}
func (mm *MinisMarketMaker) OnStartFinish(exchange security.Exchange)                {}

func (mm *MinisMarketMaker) OnTradeFromAnotherAccount(tradeFromAnotherAccount order.TradeFromAnotherAccount) {
}

///////////////// Market Data Callbacks ////////////////////////////////
func (mm *MinisMarketMaker) OnBookUpdated(bookUpdated marketdata.BookUpdated) {
	//mm.logger.Printf("New Book Update of %v\n Bids: %+v\n Asks: %+v\n", bookUpdated.Security.Symbol, bookUpdated.Book.Bids, bookUpdated.Book.Asks)
	//mm.logger.Printf("BookUpdated : %+v ", bookUpdated)

	mm.rwMutex.Lock()
	mm.automaticSpread = mm.calculateSpread(bookUpdated) //agregar en calculo de precios
	mm.rwMutex.Unlock()
	myBook := bookUpdated.Book.Bids
	if mm.side == order.Side_SELL {
		myBook = bookUpdated.Book.Asks
	}

	if len(myBook) <= 0 || myBook[0].Qty <= 0 || myBook[0].Px <= 0 {
		//myBook[0].Px = 0.0
		mm.removeOrder()
		return
	}

	// if myBook[0].Qty-int(mm.qty) == 0.0 &&
	// 	mm.activeOrder != nil &&
	// 	mm.activeOrder.Px == myBook[0].Px {
	// 	myBook[0].Px = 0.0
	// }

	mm.rwMutex.Lock()
	mm.mktPx = myBook[0].Px
	mm.px = mm.calculatePx()
	mm.qty = mm.calculateQty()
	mm.rebalance()
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnDisconnect(exchange security.Exchange)                   {}
func (mm *MinisMarketMaker) OnSecurityStatus(securityStatus marketdata.SecurityStatus) {}

///////////////// Market Maker Specific CallBacks ////////////////////////////////

func (mm *MinisMarketMaker) calculatePx() float64 {

	if mm.automaticSpreadEnabled {
		if mm.side == order.Side_BUY {
			return mm.mktPx - mm.automaticSpread
		} else {
			return mm.mktPx + mm.automaticSpread
		}
	}

	return mm.mktPx
}

func (mm *MinisMarketMaker) calculateQty() float64 {

	return mm.qtyDefault
}

func (mm *MinisMarketMaker) placeOrder() {
	request := order.PlaceOrderRequest{
		OrderId:  utils.UUID(),
		Account:  mm.account,
		Security: mm.miniSecurity,
		Qty:      mm.qty,
		Px:       mm.px,
		Side:     mm.side,
		Type:     order.Type_LIMIT,
		Validity: order.Validity_DAY,
	}

	newOrder, err := mm.broker.PlaceOrder(request, mm)
	if err != nil {
		//mm.logger.Printf("Cannot place new order: %+v. Error: %v", request, err)
		return
	}

	mm.sentOrder = newOrder
}

func (mm *MinisMarketMaker) replaceOrder() {
	request := order.ReplaceOrderRequest{
		Order: *mm.activeOrder,
		Qty:   mm.qty,
		Px:    mm.px,
	}

	err := mm.broker.ReplaceOrder(request)
	if err != nil {
		mm.logger.Printf("Cannot replace order %+v with request: %+v. Error: %s", mm.activeOrder, request, err)
		return
	}

	mm.sentOrder = mm.activeOrder
	mm.sentOrder.Px = mm.px
	mm.sentOrder.Qty = mm.qty
}

func (mm *MinisMarketMaker) rebalance() {

	if mm.pendingCancel {
		mm.logger.Printf("Rebalance, pendingCancel true")
		mm.removeOrder()
	} else if mm.unbalanced {
		mm.logger.Printf("Rebalance, position unbalanced, waiting for balancer")

	} else if !mm.enabledAll {
		mm.logger.Printf("Cannot rebalance. Robot is disabled for all assets.")
		mm.removeOrder()
	} else if !mm.enabled {
		mm.logger.Printf("Cannot rebalance. Robot is disabled.")

	} else if mm.px <= 0.0 {
		mm.logger.Println("Cannot rebalance. Px <= 0")
		mm.removeOrder()
	} else if mm.sentOrder != nil {
		mm.logger.Println("Cannot rebalance. Another action is pending acknowledgement")
	} else if mm.activeOrder == nil && mm.sentOrder == nil {

		mm.placeOrder()
	} else if mm.activeOrder.Px != mm.px || (mm.activeOrder.Qty-mm.activeOrder.CumQty) != mm.qty {
		mm.replaceOrder()
		mm.logger.Printf("active order: %+v\n MM Px: %+v\n MM qty: %v\n", mm.activeOrder, mm.px, mm.qty)
	}
}

func (mm *MinisMarketMaker) cancelOrder() {
	mm.logger.Printf("CancelOrder has been called")
	request := order.CancelOrderRequest{
		Order: *mm.activeOrder,
	}
	err := mm.broker.CancelOrder(request)
	if err != nil {
		mm.logger.Printf("Cannot cancel order %+v. Error %v", mm.activeOrder, err)
	}
	mm.sentOrder = mm.activeOrder
}

func (mm *MinisMarketMaker) invalidEvent(event order.OrderEvent) bool {
	invalidOrder := (mm.activeOrder == nil && mm.sentOrder == nil)
	invalidOrder = invalidOrder ||
		((mm.activeOrder != nil && mm.activeOrder.Id != event.Order.Id) && (mm.sentOrder != nil && mm.sentOrder.Id != event.Order.Id))
	if invalidOrder {
		mm.logger.Printf("Received order event for an unknown order: %+v", event)
	}
	return invalidOrder
}

func (mm *MinisMarketMaker) removeOrder() {
	if mm.activeOrder != nil {
		if mm.sentOrder != nil {
			mm.pendingCancel = true
		} else if !mm.cancelRejected {
			mm.cancelOrder()
		}
	}
}

func (mm *MinisMarketMaker) OnSyntheticPositionChange(syntheticInstrument string, event position.PositionEvent) {
	mm.logger.Printf("Synthetic Position %s: %+v\n", syntheticInstrument, event)
	mm.rwMutex.Lock()
	mm.unbalanced = false
	if event.NewPosition.NetQty >= 60 || event.NewPosition.NetQty <= -60 {
		mm.unbalanced = true
		mm.px = mm.calculatePx()
		mm.qty = mm.calculateQty()
		mm.rebalance()
	}
	mm.rwMutex.Unlock()
}

// settings callbacks ///

func (mm *MinisMarketMaker) OnBotSettingChange(botSetting settings.BotSetting) {} //chequear

func (mm *MinisMarketMaker) OnBotEnabledChange(botEnabled settings.Enabled) {
	mm.logger.Printf("%v OnBotEnabledChange %+v\n", mm.miniSecurity.Symbol, botEnabled)
	mm.rwMutex.Lock()
	if mm.enabledAll {
		if !botEnabled.Value {
			mm.enabledAll = false
		}
	} else {
		if botEnabled.Value {
			//mm.logger.Printf("Activating. Bot is enabled")
			mm.enabledAll = true
		}

	}
	mm.px = mm.calculatePx()
	mm.qty = mm.calculateQty()
	mm.rebalance()
	mm.rwMutex.Unlock()
}

func (mm *MinisMarketMaker) OnCommand(command settings.FrontCommand) {}

func (mm *MinisMarketMaker) OnAssetSettingChange(assetSetting settings.AssetSetting) {
	if assetSetting.Asset != mm.miniSecurity.Symbol {
		mm.logger.Printf("Ignoring asset event %+v: %+v", &mm.miniSecurity.Symbol, assetSetting)
		return
	}

	rebalance := false
	switch assetSetting.Key {
	case settings.SWITCH_ASSET_BID:
		mm.logger.Printf("%+v Switch asset bid: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_BUY {
			mm.switchState(assetSetting)
		}

	case settings.SWITCH_ASSET_ASK:
		mm.logger.Printf("%+v Switch asset ask: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_SELL {
			mm.switchState(assetSetting)
		}

	case settings.CHANGE_VOL_BID:
		mm.logger.Printf("%+v Change vol bid: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_BUY {
			if assetSetting.Value >= 0.0 {
				rebalance = true
				mm.rwMutex.Unlock()
			}
		}

	case settings.CHANGE_VOL_ASK:
		mm.logger.Printf("%+v Change vol ask: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_SELL {
			if assetSetting.Value >= 0.0 {
				rebalance = true
			}
		}

	case settings.CHANGE_QTY_BID:
		mm.logger.Printf("%+v Change qty bid: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_BUY {
			if assetSetting.Value >= 0.0 {
				mm.rwMutex.Lock()
				mm.qty = assetSetting.Value
				rebalance = true
				mm.rwMutex.Unlock()
			}
		}

	case settings.CHANGE_QTY_ASK:
		mm.logger.Printf("%+v Change qty ask: %+v", &mm.miniSecurity.Symbol, assetSetting)
		if mm.side == order.Side_SELL {
			if assetSetting.Value >= 0.0 {
				mm.rwMutex.Lock()
				mm.qty = assetSetting.Value
				rebalance = true
				mm.rwMutex.Unlock()
			}
		}
	default:
		mm.logger.Printf("%+v Asset setting key not recognized: %+v", &mm.miniSecurity.Symbol, assetSetting)
	}

	if rebalance {
		mm.rwMutex.Lock()
		mm.px = mm.calculatePx()
		mm.qty = mm.calculateQty()
		mm.rebalance()
		mm.rwMutex.Unlock()
	} else {
		//mm.logger.Printf("Ignoring rebalance asset settings event: %+v", botSetting)
	}

}

func (mm *MinisMarketMaker) deactivate(notify string) {
	if mm.enabled {
		switch notify {
		case "asset":
			mm.enabled = false
			if mm.side == order.Side_SELL {
				mm.settingsManager.ChangeAssetState(settings.SWITCH_ASSET_ASK, 0, mm.miniSecurity.Symbol)
			} else {
				mm.settingsManager.ChangeAssetState(settings.SWITCH_ASSET_BID, 0, mm.miniSecurity.Symbol)
			}

		case "global":
			mm.enabledAll = false
			mm.settingsManager.ChangeRobotState(0)
		default:
			mm.logger.Printf(notify)
			//mm.traderUpdater.SendToast(notify)
		}
	}
	mm.removeOrder()
}

func (mm *MinisMarketMaker) switchState(assetSetting settings.AssetSetting) {
	if mm.enabled {
		if assetSetting.Value == 0 {
			mm.rwMutex.Lock()
			mm.deactivate("asset")
			mm.enabled = false
			mm.rwMutex.Unlock()
		}
	} else {
		if assetSetting.Value == 1 {
			mm.rwMutex.Lock()
			mm.logger.Printf("Activating market maker %v %v\n", mm.miniSecurity.Symbol, mm.side)
			mm.enabled = true
			mm.rwMutex.Unlock()
		}
	}
}

func (mm *MinisMarketMaker) calculateSpread(bookUpdated marketdata.BookUpdated) float64 {
	//evaluar escenario donde solo hay una punta (colocar spread de 0,5)

	spread := 0.0
	if bookUpdated.Book.Asks[0].Px == 0.0 && bookUpdated.Book.Bids[0].Px == 0.0 {
		return spread
	}

	if bookUpdated.Book.Asks[0].Px == 0.0 || bookUpdated.Book.Bids[0].Px == 0.0 {
		spread = 0.5
		return spread
	}

	futureSpread := bookUpdated.Book.Asks[0].Px - bookUpdated.Book.Bids[0].Px
	if futureSpread > 1 {
		spread = 0.3
	} else if futureSpread < 0.9 {
		spread = 0.1
	}

	return spread
}
