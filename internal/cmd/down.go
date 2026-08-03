package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/khorost-space/space-lab/internal/dockerx"
)

// Down останавливает стек. purge удаляет и тома.
//
// Без purge том identity и данные postgres остаются: повторный up тогда
// поднимает тот же мир с той же историей сигналов, и это чаще то, что нужно.
// Полная очистка — явное действие, а не умолчание: у студента там журнал
// собственных попыток.
func Down(ctx context.Context, dir string, purge bool, stdout io.Writer) error {
	args := []string{"down"}
	if purge {
		args = append(args, "-v")
	}
	if err := dockerx.Compose(ctx, dir, args...); err != nil {
		return fmt.Errorf("остановить стек: %w", err)
	}
	if purge {
		_, _ = fmt.Fprintln(stdout, "Стек остановлен, тома удалены")
	} else {
		_, _ = fmt.Fprintln(stdout, "Стек остановлен, тома сохранены")
	}
	return nil
}
