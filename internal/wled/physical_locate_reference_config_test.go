package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPhysicalLocateFiveControllerReferenceConfiguration(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	controllerIDs := []int64{f.controller}
	capacities := []int64{200, 250, 150, 300, 100}
	stringLengths := [][]int64{{120, 80}, {125, 125}, {75, 75}, {150, 150}, {40, 60}}
	mocks := []*physicalMock{configureReferencePhysicalMock(t, f.mock, capacities[0])}

	for index := 0; index < 5; index++ {
		var mock *physicalMock
		if index == 0 {
			mock = mocks[0]
		} else {
			var controllerID int64
			controllerID, mock = addReferenceController(t, f, index, capacities[index])
			controllerIDs = append(controllerIDs, controllerID)
			mocks = append(mocks, mock)
		}
		if index == 0 {
			controllerIDs[0] = f.controller
		}
		addReferenceWiring(t, f, controllerIDs[index], index, stringLengths[index])
		if err := f.service.SaveControllerMappingVerification(ctx, controllerIDs[index], true); err != nil {
			t.Fatalf("verify controller %d against local mock WLED: %v", index+1, err)
		}
	}

	var topology cabinetpixels.Topology
	if err := f.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		topology, err = cabinetpixels.Load(ctx, q, 1)
		return err
	}); err != nil {
		t.Fatalf("load five-controller reference topology: %v", err)
	}
	if topology.Total != 1000 || len(topology.Controllers) != 5 {
		t.Fatalf("reference topology total/controllers = %d/%d, want 1000/5", topology.Total, len(topology.Controllers))
	}
	for index, controller := range topology.Controllers {
		if controller.Record.ID != controllerIDs[index] || controller.Pixels != capacities[index] || len(controller.Strings) != 2 {
			t.Fatalf("controller %d topology = %+v, want id %d capacity %d with two strings", index+1, controller, controllerIDs[index], capacities[index])
		}
		for stringIndex, string := range controller.Strings {
			if string.Record.PixelCount != stringLengths[index][stringIndex] || len(string.Sections) != 2 {
				t.Fatalf("controller %d string %d = %+v, want %d pixels/two serpentine sections", index+1, stringIndex+1, string, stringLengths[index][stringIndex])
			}
			var sectionPixels int64
			for _, section := range string.Sections {
				sectionPixels += section.PixelCount
			}
			if sectionPixels != string.Record.PixelCount {
				t.Fatalf("controller %d string %d sections total %d, want %d", index+1, stringIndex+1, sectionPixels, string.Record.PixelCount)
			}
		}
	}

	cases := []struct {
		name        string
		start       int64
		count       int64
		pieces      []cabinetpixels.PhysicalPiece
		controllers []int64
		commands    []struct {
			controller int
			segment    int
			start      int
			stop       int
		}
	}{
		{
			name:  "cabinet pixels 25-34",
			start: 25, count: 10,
			pieces:      []cabinetpixels.PhysicalPiece{{ControllerID: controllerIDs[0], StringID: topology.Controllers[0].Strings[0].Record.ID, CabinetStart: 25, LocalStart: 25, StringStart: 25, Count: 10}},
			controllers: []int64{controllerIDs[0]},
			commands:    []struct{ controller, segment, start, stop int }{{0, 10, 24, 34}},
		},
		{
			name:  "cabinet pixels 195-210 across controllers and strings",
			start: 195, count: 16,
			pieces: []cabinetpixels.PhysicalPiece{
				{ControllerID: controllerIDs[0], StringID: topology.Controllers[0].Strings[1].Record.ID, CabinetStart: 195, LocalStart: 195, StringStart: 75, Count: 6},
				{ControllerID: controllerIDs[1], StringID: topology.Controllers[1].Strings[0].Record.ID, CabinetStart: 201, LocalStart: 1, StringStart: 1, Count: 10},
			},
			controllers: []int64{controllerIDs[0], controllerIDs[1]},
			commands:    []struct{ controller, segment, start, stop int }{{0, 20, 94, 100}, {1, 10, 0, 10}},
		},
		{
			name:  "cabinet pixels 225-234",
			start: 225, count: 10,
			pieces:      []cabinetpixels.PhysicalPiece{{ControllerID: controllerIDs[1], StringID: topology.Controllers[1].Strings[0].Record.ID, CabinetStart: 225, LocalStart: 25, StringStart: 25, Count: 10}},
			controllers: []int64{controllerIDs[1]},
			commands:    []struct{ controller, segment, start, stop int }{{1, 10, 24, 34}},
		},
		{
			name:  "cabinet pixels 445-460 across controllers and strings",
			start: 445, count: 16,
			pieces: []cabinetpixels.PhysicalPiece{
				{ControllerID: controllerIDs[1], StringID: topology.Controllers[1].Strings[1].Record.ID, CabinetStart: 445, LocalStart: 245, StringStart: 120, Count: 6},
				{ControllerID: controllerIDs[2], StringID: topology.Controllers[2].Strings[0].Record.ID, CabinetStart: 451, LocalStart: 1, StringStart: 1, Count: 10},
			},
			controllers: []int64{controllerIDs[1], controllerIDs[2]},
			commands:    []struct{ controller, segment, start, stop int }{{1, 20, 119, 125}, {2, 10, 0, 10}},
		},
		{
			name:  "cabinet pixels 895-910 across controllers and strings",
			start: 895, count: 16,
			pieces: []cabinetpixels.PhysicalPiece{
				{ControllerID: controllerIDs[3], StringID: topology.Controllers[3].Strings[1].Record.ID, CabinetStart: 895, LocalStart: 295, StringStart: 145, Count: 6},
				{ControllerID: controllerIDs[4], StringID: topology.Controllers[4].Strings[0].Record.ID, CabinetStart: 901, LocalStart: 1, StringStart: 1, Count: 10},
			},
			controllers: []int64{controllerIDs[3], controllerIDs[4]},
			commands:    []struct{ controller, segment, start, stop int }{{3, 20, 144, 150}, {4, 10, 0, 10}},
		},
	}

	drawers := make([]int64, len(cases))
	bins := make([]int64, len(cases))
	for index, test := range cases {
		drawerID, err := f.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: 1, Label: test.name, DisplayOrder: int64(index + 10), X: float64(index * 10), Width: 5, Height: 5})
		if err != nil {
			t.Fatalf("create drawer for %s: %v", test.name, err)
		}
		binID, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: test.name, Width: sql.NullInt64{Int64: 1, Valid: true}})
		if err != nil {
			t.Fatalf("create bin for %s: %v", test.name, err)
		}
		if err := f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: binID, DrawerID: drawerID, Width: 1, Height: 1}); err != nil {
			t.Fatalf("attach bin for %s: %v", test.name, err)
		}
		if _, err := f.store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: 1, DrawerID: drawerID, StartPixel: test.start, PixelCount: test.count}); err != nil {
			t.Fatalf("allocate %s: %v", test.name, err)
		}
		drawers[index] = drawerID
		bins[index] = binID
	}

	for caseIndex, test := range cases {
		pieces, err := cabinetpixels.SplitTopologyRange(topology, test.start, test.count)
		if err != nil {
			t.Fatalf("split %s: %v", test.name, err)
		}
		if !reflect.DeepEqual(pieces, test.pieces) {
			t.Fatalf("%s physical destination pieces = %+v, want %+v", test.name, pieces, test.pieces)
		}
		session, err := f.service.LocateDrawerBin(ctx, drawers[caseIndex], bins[caseIndex], 7)
		if err != nil {
			t.Fatalf("Locate %s: %v", test.name, err)
		}
		if session.State != "active" || !reflect.DeepEqual(session.AffectedControllerIDs, test.controllers) {
			t.Fatalf("Locate %s session = %+v, want active targets %v", test.name, session, test.controllers)
		}
		for _, want := range test.commands {
			posts := mocks[want.controller].posts()
			if len(posts) == 0 {
				t.Fatalf("Locate %s recorded no WLED payload for controller %d", test.name, want.controller+1)
			}
			assertPixelCommand(t, posts[len(posts)-1], want.segment, want.start, want.stop, []any{float64(128), float64(0), float64(0)})
		}
	}
	if err := f.service.StopDrawer(ctx, drawers[len(drawers)-1], 7); err != nil {
		t.Fatalf("stop final reference Locate: %v", err)
	}
}

func addReferenceController(t *testing.T, f *physicalFixture, index int, capacity int64) (int64, *physicalMock) {
	t.Helper()
	mock := configureReferencePhysicalMock(t, newPhysicalMock(), capacity)
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	controller, err := f.store.CreateController(context.Background(), db.CreateControllerParams{Name: fmt.Sprintf("Reference controller %d", index+1), IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(context.Background(), `UPDATE controllers SET led_count=? WHERE id=?`, capacity, controller.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateCabinetController(context.Background(), db.CreateCabinetControllerParams{CabinetID: 1, ControllerID: controller.ID, Slot: int64(index)}); err != nil {
		t.Fatal(err)
	}
	return controller.ID, mock
}

func configureReferencePhysicalMock(t *testing.T, mock *physicalMock, capacity int64) *physicalMock {
	t.Helper()
	if capacity%2 != 0 {
		t.Fatalf("mock output capacity %d must divide evenly into two segments", capacity)
	}
	half := capacity / 2
	mock.count = int(capacity)
	mock.config = fmt.Sprintf(`{"hw":{"led":{"ins":[{"start":0,"len":%d,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":%d,"len":%d,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`, half, half, half)
	state, err := json.Marshal(map[string]any{"on": true, "seg": []map[string]any{
		{"id": 10, "start": 0, "stop": half, "on": true, "fx": 0, "grp": 1, "spc": 0, "rev": false, "mi": false, "col": [][]int{{20, 30, 40}, {0, 0, 0}, {0, 0, 0}}},
		{"id": 20, "start": half, "stop": int(capacity), "on": true, "fx": 0, "grp": 1, "spc": 0, "rev": false, "mi": false, "col": [][]int{{50, 60, 70}, {0, 0, 0}, {0, 0, 0}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mock.state = string(state)
	return mock
}

func addReferenceWiring(t *testing.T, f *physicalFixture, controllerID int64, controllerIndex int, lengths []int64) {
	t.Helper()
	if _, err := f.database.ExecContext(context.Background(), `UPDATE cabinet_controllers SET strings_configured=1 WHERE cabinet_id=1 AND controller_id=?`, controllerID); err != nil {
		t.Fatal(err)
	}
	for stringIndex, length := range lengths {
		name := fmt.Sprintf("C%d string %c", controllerIndex+1, 'A'+stringIndex)
		result, err := f.database.ExecContext(context.Background(), `INSERT INTO cabinet_led_strings (cabinet_id,controller_id,name,display_order,pixel_count,layout_complete) VALUES (?,?,?,?,?,1)`, 1, controllerID, name, stringIndex, length)
		if err != nil {
			t.Fatalf("create explicit physical string %q: %v", name, err)
		}
		stringID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		firstSectionLength := length / 2
		for sectionIndex := 0; sectionIndex < 2; sectionIndex++ {
			sectionLength := firstSectionLength
			points := `[{"x":5,"y":5},{"x":95,"y":5}]`
			if sectionIndex == 1 {
				sectionLength = length - firstSectionLength
				points = `[{"x":95,"y":10},{"x":5,"y":10}]`
			}
			if _, err := f.database.ExecContext(context.Background(), `INSERT INTO cabinet_led_path_sections (string_id,display_order,pixel_count,points_json) VALUES (?,?,?,?)`, stringID, sectionIndex, sectionLength, points); err != nil {
				t.Fatalf("create serpentine section %d for %q: %v", sectionIndex+1, name, err)
			}
		}
	}
}
