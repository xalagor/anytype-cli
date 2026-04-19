package doctor

import (
	"github.com/spf13/cobra"

	duplicatesCmd "github.com/anyproto/anytype-cli/cmd/doctor/duplicates"
	imagesCmd "github.com/anyproto/anytype-cli/cmd/doctor/images"
)

func NewDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor <command>",
		Short: "Diagnose your Anytype data",
		Long:  "Run diagnostics on your Anytype spaces to find data quality issues",
	}

	cmd.AddCommand(duplicatesCmd.NewDuplicatesCmd())
	cmd.AddCommand(imagesCmd.NewImagesCmd())

	return cmd
}
