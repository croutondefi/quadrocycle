package core

import (
	log "github.com/sirupsen/logrus"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

func LoadComment(cell *cell.Cell) string {
	if cell == nil {
		return ""
	}
	l := cell.BeginParse()
	if val, err := l.LoadUInt(32); err == nil && val == 0 {
		str, err := l.LoadStringSnake()
		if err != nil {
			log.Errorf("load comment error: %v", err)
			return ""
		}
		return str
	}
	return ""
}
