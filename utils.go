package minis

import (
	"strings"

	"github.com/deltafund/components-support/position"
)

type SpreadData struct {
	SymbolPase    string
	SymbolEarly   string
	SymbolLate    string
	LocalLegSize  int64
	Product       string
	PriorityEarly int
	PriorityLate  int
}

func MiniToStdName(ticker string) string {
	ticker = strings.Replace(ticker, "MIN", "ROS", -1)
	return ticker
}

//func (mm *MinisMarketMaker) balanceContractQty(security security.Security, event position.PositionEvent, tradedPx float64) {
//chequear posiciones sinteticas para comparar contra mm.newHistoricalPos
// 	marketMaker := *mm
// 	if mm.newHistoricalPos >= 6 {
// 		mm.rwMutex.Lock()
// 		mm.miniSecurity.Symbol = MiniToStdName(mm.miniSecurity.Symbol)
// 		mm.qty = 1
// 		mm.side = order.Side_SELL
// 		mm.rebalance()
// 		mm.rwMutex.Unlock()
// 	} else if mm.newHistoricalPos <= -6 {
// 		mm.rwMutex.Lock()
// 		mm.miniSecurity.Symbol = MiniToStdName(mm.miniSecurity.Symbol)
// 		mm.qty = 1
// 		mm.side = order.Side_BUY
// 		mm.rebalance()
// 		mm.rwMutex.Unlock()
// 	}
// 	//volver a valores anteriores de mm y quantity (o no usar mm, sino otro componente)
// 	mm = &marketMaker
//}

type NetFuturePosition struct {
	netFutureSymbol string
	signs           map[string]float64
	products        map[string]string
	position        position.Position
	historical      position.Position
}

type PositionEvent struct {
	Symbol                string
	OldPosition           position.Position
	NewPosition           position.Position
	OldHistoricalPosition position.Position
	NewHistoricalPosition position.Position
}
