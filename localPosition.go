package minis

import (
	"fmt"

	"github.com/deltafund/api-fix/order"
	"github.com/deltafund/api-fix/security"
	"github.com/deltafund/components-support/position"
)

const (
	NET_FUTURE_POSITION string = "net-future-position"
	TONS_POSITION       string = "tons-position"
)

type netFuturePos struct {
	stdSecurity  security.Security
	miniSecurity security.Security
	position     position.Position
}

func NewNetFuturePos(stdSecurity security.Security, miniSecurity security.Security, initPosition position.Position) *netFuturePos {
	return &netFuturePos{
		stdSecurity:  stdSecurity,
		miniSecurity: miniSecurity,
		position:     initPosition,
	}
}

func (nfp *netFuturePos) ConsumeExecution(
	orderEvent order.OrderEvent,
	securityPositions map[string]position.Position,
	syntheticPositions map[string]position.Position,
	historicalSecurityPositions map[string]position.Position,
	historicalSyntheticPositions map[string]position.Position,
) *position.PositionEvent {

	fmt.Printf("%s ConsumeExecution OrderEvent %+v\n", nfp.miniSecurity.Symbol, orderEvent)

	oldPosition := nfp.position
	switch orderEvent.Order.Security.Symbol {
	case nfp.stdSecurity.Symbol:

		if orderEvent.ExecutionReport.Side == order.Side_BUY {
			nfp.position.BuyQty += (orderEvent.Qty * 100)
			//nfp.position.AvgBuyPx = (nfp.position.AvgBuyPx + avgPx) / nfp.position.BuyQty
		}

		if orderEvent.ExecutionReport.Side == order.Side_SELL {
			nfp.position.SellQty += (orderEvent.Qty * 100)
			//nfp.position.AvgSellPx = (nfp.position.AvgSellPx + avgPx) / nfp.position.SellQty
		}
	case nfp.miniSecurity.Symbol:
		if orderEvent.ExecutionReport.Side == order.Side_BUY {
			nfp.position.BuyQty += (orderEvent.Qty * 10)
			//nfp.position.AvgBuyPx = (nfp.position.AvgBuyPx + avgPx) / nfp.position.BuyQty
		}

		if orderEvent.ExecutionReport.Side == order.Side_SELL {
			nfp.position.SellQty += (orderEvent.Qty * 10)
			//nfp.position.AvgSellPx = (nfp.position.AvgSellPx + avgPx) / nfp.position.SellQty
		}

	default:
		//ignore trade
	}
	nfp.position.NetQty = nfp.position.BuyQty - nfp.position.SellQty
	//en las cantidades de OrderEvent, sumar o restar cantidades a positionEvent (newHistoricalPos)
	//return &position.PositionEvent{}
	return &position.PositionEvent{
		OldPosition: oldPosition,
		NewPosition: nfp.position,
	}
}

type TonsPosition struct {
	security security.Security
	position position.Position
}

func NewTonsPosition(security security.Security, position position.Position) *TonsPosition {
	return &TonsPosition{
		security: security,
		position: position}
}

func (tp *TonsPosition) ConsumeExecution(
	orderEvent order.OrderEvent,
	securityPositions map[string]position.Position,
	syntheticPositions map[string]position.Position,
	historicalSecurityPositions map[string]position.Position,
	historicalSyntheticPositions map[string]position.Position,
) *position.PositionEvent {

	//fmt.Printf("%s TonsPosition ConsumeExecution OrderEvent %+v\n", &tp.security.Symbol, orderEvent)
	fmt.Println("tp.Security.symbol : ", tp.security.Symbol, "order event sec symbol : ", orderEvent.Order.Security.Symbol)
	if orderEvent.Order.Security.Symbol != tp.security.Symbol {
		fmt.Println("nil")
		return nil
	}

	oldPosition := tp.position
	sizePerContract := 100.0
	if tp.security.Harbour == "MIN" {
		sizePerContract = 10.0
	}

	if orderEvent.Order.Side == order.Side_BUY {
		tp.position.BuyQty += (orderEvent.ExecutionReport.Qty * sizePerContract)
	} else {
		tp.position.SellQty += (orderEvent.ExecutionReport.Qty * sizePerContract)
	}
	tp.position.NetQty = tp.position.BuyQty - tp.position.SellQty

	fmt.Printf("OldPosition %+v\n newPos : %+v\n", oldPosition, tp.position)
	return &position.PositionEvent{
		OldPosition: oldPosition,
		NewPosition: tp.position,
	}
}

func StartSubscriptions(settingsManager *settings.SettingsManager, myBroker broker.DefaultBroker, positionManager position.IPositionManager, sec security.Security, stdFuture security.Security, mmBuys *marketmaker.MinisMarketMaker, mmSells *marketmaker.MinisMarketMaker, balancers *balancer.Balancer) {
	positionManager.SubscribeSyntheticPosition(sec.Symbol+"-"+NET_FUTURE_POSITION, mmBuys)
	positionManager.SubscribeSyntheticPosition(sec.Symbol+"-"+NET_FUTURE_POSITION, mmSells)
	positionManager.SubscribeSyntheticPosition(sec.Symbol+"-"+NET_FUTURE_POSITION, balancers)

	myBroker.SubscribeSymbolSide(sec, order.Side_BUY, balancers)
	myBroker.SubscribeSymbolSide(sec, order.Side_SELL, balancers)

	myBroker.SubscribeBook(stdFuture, mmBuys)
	myBroker.SubscribeBook(stdFuture, mmSells)

	myBroker.SubscribeExchange(security.Exchange_ROFEX, mmBuys)
	myBroker.SubscribeExchange(security.Exchange_ROFEX, mmSells)
	myBroker.SubscribeExchange(security.Exchange_ROFEX, balancers)

	//positionSaver tiene metodos para traer mapas con posiciones (para enviar como initPos en NewNetFuturePos..)
	positionManager.SubscribeSecurityPosition(sec, mmBuys)
	positionManager.SubscribeSecurityPosition(sec, mmSells)

	settingsManager.Subscribe(mmBuys)
	settingsManager.Subscribe(mmSells)
	settingsManager.Subscribe(balancers)

}
