// Package cmd — реализации подкоманд space-lab.
package cmd

import (
	"fmt"
	"io"

	"github.com/khorost-space/space-lab/internal/project"
)

// Init выполняет «space-lab init» в каталоге dir.
func Init(dir, name string, stdout io.Writer) error {
	c, err := project.Init(dir, name)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Проект готов: %s, состояние в %s/\n", project.ConfigFile, project.StateDir)
	_, _ = fmt.Fprintf(stdout, "Объект: %s, образы платформы: %s (версия %s)\n",
		c.Object.Name, c.Platform.Registry, c.Platform.Version)
	return nil
}
