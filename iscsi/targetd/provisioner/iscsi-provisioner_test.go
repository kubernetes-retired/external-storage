package provisioner

import (
	"github.com/spf13/viper"
	"testing"
)

func TestZeroExports(t *testing.T) {
	viper.SetDefault("log-level", "debug")
	initLog()
	var emptyExportList exportList
	lun, err := getFirstAvailableLun(emptyExportList)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 0 {
		t.Fatal("lun should have been 0 and it was: ", lun)
	}
}

func TestFindGaps1Initiator(t *testing.T) {
	exportListWithGap := []export{export{
		Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
	}
	lun, err := getFirstAvailableLun(exportListWithGap)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 2 {
		t.Fatal("lun should have been 2 and it was: ", lun)
	}
}

func TestFindGaps2Initiators(t *testing.T) {
	exportListWithGap := []export{export{
		Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
	}
	lun, err := getFirstAvailableLun(exportListWithGap)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 2 {
		t.Fatal("lun should have been 2 and it was: ", lun)
	}

}

func TestFindGaps3Initiators(t *testing.T) {
	exportListWithGap := []export{export{
		Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
	}
	lun, err := getFirstAvailableLun(exportListWithGap)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 2 {
		t.Fatal("lun should have been 2 and it was: ", lun)
	}

}

func TestFindGaps5Initiators(t *testing.T) {
	exportListWithGap := []export{export{
		Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
		export{
			Lun: 0},
		export{
			Lun: 1},
		export{
			Lun: 3},
	}
	lun, err := getFirstAvailableLun(exportListWithGap)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 2 {
		t.Fatal("lun should have been 2 and it was: ", lun)
	}

}

func Test255Luns(t *testing.T) {
	exportList := make([]export, 255, 255)
	for i, _ := range exportList {
		exportList[i] = export{
			Lun: int32(i),
		}
	}
	lun, err := getFirstAvailableLun(exportList)
	if err == nil {
		t.Fatal("function should have returned error, it returned: ", lun)
	}
}
func Test255Luns2Initiators(t *testing.T) {
	exportList := make([]export, 510, 510)
	for i, _ := range exportList {
		exportList[i] = export{
			Lun: int32(i) << 1,
		}
	}
	lun, err := getFirstAvailableLun(exportList)
	if err == nil {
		t.Fatal("function should have returned error, it returned: ", lun)
	}
}

func Test250Luns2Initiators(t *testing.T) {
	exportList := make([]export, 500, 500)
	for i, _ := range exportList {
		exportList[i] = export{
			Lun: int32(i / 2),
		}
	}
	lun, err := getFirstAvailableLun(exportList)
	if err != nil {
		t.Fatal(err)
	}
	if lun != 250 {
		t.Fatal("lun should have been 250 and it was: ", lun)
	}
}
