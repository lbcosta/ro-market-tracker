package web

import (
	"fmt"
	"strings"
)

// naviCommand monta o comando de cliente "/navi <mapa>/<x>/<y>" que o RO usa
// para traçar rota até um ponto do mapa. mapName vem do detalhe da loja como
// "prt_mk.gat" (com a extensão do arquivo de mapa); o comando espera só o
// nome, sem extensão.
func naviCommand(mapName, x, y string) string {
	mapName = strings.TrimSuffix(mapName, ".gat")
	return fmt.Sprintf("/navi %s/%s/%s", mapName, x, y)
}
