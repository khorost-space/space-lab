package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

// FormatStatus печатает витрину для студента: имя объекта, состояние, каким
// его видит мир, номер и время последнего сигнала, заявленную версию образа.
//
// Строк ровно столько, сколько полей во View: status — не диагностика (это
// задача check из шага 10), а прямой пересказ витрины, единственного места,
// где студент видит вердикт мира о своём аппарате.
func FormatStatus(v worldapi.View) string {
	seen := "сигналов не было"
	if v.LastSeen != nil {
		seen = v.LastSeen.Format(time.RFC3339)
	}
	return fmt.Sprintf(
		"Объект:  %s\nСостояние: %s\nПоследний сигнал: №%d, %s\nВерсия образа: %s\n",
		v.Name, v.Condition, v.LastSequence, seen, v.ServedVersion,
	)
}

// Status выполняет «space-lab status» в каталоге dir: читает конфигурацию,
// собирает клиент витрины на http://localhost:<Ports.API> и печатает
// FormatStatus.
//
// Токен не нужен: витрина публична (Showcase не требует Authorization), и
// передавать секрет туда, где его не проверяют, — лишний риск без пользы.
func Status(ctx context.Context, dir string, stdout io.Writer) error {
	cfg, err := project.Load(dir)
	if err != nil {
		return err
	}

	hc := &http.Client{Timeout: 5 * time.Second}
	world := worldapi.New(fmt.Sprintf("http://localhost:%d", cfg.Ports.API), "", hc)
	v, err := world.Showcase(ctx)
	if err != nil {
		return fmt.Errorf("прочитать витрину: %w", err)
	}

	_, _ = fmt.Fprint(stdout, FormatStatus(v))
	return nil
}
