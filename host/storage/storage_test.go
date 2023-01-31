package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.sia.tech/hostd/host/storage"
	"go.sia.tech/hostd/internal/merkle"
	"go.sia.tech/hostd/internal/persist/sqlite"
	"lukechampine.com/frand"
)

func TestAddVolume(t *testing.T) {
	const expectedSectors = 500
	dir := t.TempDir()

	db, err := sqlite.OpenDatabase(filepath.Join(dir, "hostd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	vm := storage.NewVolumeManager(db)

	volume, err := vm.AddVolume(filepath.Join(t.TempDir(), "hostdata.dat"), expectedSectors)
	if err != nil {
		t.Fatal(err)
	}

	volumes, err := vm.Volumes()
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case len(volumes) != 1:
		t.Fatalf("expected 1 volumes, got %v", len(volumes))
	case volumes[0].ID != volume.ID:
		t.Fatalf("expected volume %v, got %v", volume.ID, volumes[0].ID)
	case volumes[0].TotalSectors != expectedSectors:
		t.Fatalf("expected %v total sectors, got %v", expectedSectors, volumes[0].TotalSectors)
	case volumes[0].UsedSectors != 0:
		t.Fatalf("expected 0 used sectors, got %v", volumes[0].UsedSectors)
	case volumes[0].ReadOnly:
		t.Fatal("expected volume to be writable")
	}
}

func TestRemoveVolume(t *testing.T) {
	const expectedSectors = (1 << 40) / (1 << 22) // 1 TiB
	dir := t.TempDir()

	// create the database
	db, err := sqlite.OpenDatabase(filepath.Join(dir, "hostd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// initialize the storage manager
	vm := storage.NewVolumeManager(db)
	volume, err := vm.AddVolume(filepath.Join(t.TempDir(), "hostdata.dat"), expectedSectors)
	if err != nil {
		t.Fatal(err)
	}

	sector := make([]byte, 1<<22)
	if _, err := frand.Read(sector[:256]); err != nil {
		t.Fatal(err)
	}
	root := storage.SectorRoot(merkle.SectorRoot(sector))

	// write the sector
	release, err := vm.Write(root, sector)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// attempt to remove the volume. Should return ErrNotEnoughStorage since
	// there is only one volume.
	if err := vm.RemoveVolume(volume.ID, false); !errors.Is(err, storage.ErrNotEnoughStorage) {
		t.Fatalf("expected ErrNotEnoughStorage, got %v", err)
	}

	// remove the sector
	if err := vm.RemoveSector(root); err != nil {
		t.Fatal(err)
	}

	// remove the volume
	if err := vm.RemoveVolume(volume.ID, false); err != nil {
		t.Fatal(err)
	}
}

func TestVolumeShrink(t *testing.T) {
	const sectors = 64
	dir := t.TempDir()

	// create the database
	db, err := sqlite.OpenDatabase(filepath.Join(dir, "hostd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// initialize the storage manager
	vm := storage.NewVolumeManager(db)
	volume, err := vm.AddVolume(filepath.Join(t.TempDir(), "hostdata.dat"), sectors)
	if err != nil {
		t.Fatal(err)
	}

	roots := make([]storage.SectorRoot, 0, sectors)
	// fill the volume
	for i := 0; i < cap(roots); i++ {
		sector := make([]byte, 1<<22)
		if _, err := frand.Read(sector[:256]); err != nil {
			t.Fatal(err)
		}
		root := storage.SectorRoot(merkle.SectorRoot(sector))
		release, err := vm.Write(root, sector)
		if err != nil {
			t.Fatal(i, err)
		}
		defer release()
		roots = append(roots, root)

		// validate the volume stats are correct
		volumes, err := vm.Volumes()
		if err != nil {
			t.Fatal(err)
		}
		if volumes[0].UsedSectors != uint64(i+1) {
			t.Fatalf("expected %v used sectors, got %v", i+1, volumes[0].UsedSectors)
		} else if err := release(); err != nil {
			t.Fatal(err)
		}
	}

	// validate that each sector was stored in the expected location
	for i, root := range roots {
		loc, release, err := db.SectorLocation(root)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if loc.Volume != volume.ID {
			t.Fatal(err)
		} else if loc.Index != uint64(i) {
			t.Fatalf("expected sector %v to be at index %v, got %v", root, i, loc.Index)
		} else if err := release(); err != nil {
			t.Fatal(err)
		}
	}

	// try to shrink the volume, should fail since no space is available
	toRemove := sectors / 4
	remainingSectors := uint64(sectors - toRemove)
	if err := vm.ResizeVolume(context.Background(), volume.ID, remainingSectors); !errors.Is(err, storage.ErrNotEnoughStorage) {
		t.Fatalf("expected not enough storage error, got %v", err)
	}

	// remove some sectors from the beginning of the volume
	for _, root := range roots[:toRemove] {
		if err := vm.RemoveSector(root); err != nil {
			t.Fatal(err)
		}
	}
	// when shrinking, the roots after the target size should be moved to
	// the beginning of the volume
	roots = append(roots[remainingSectors:], roots[toRemove:remainingSectors]...)

	// shrink the volume by the number of sectors removed, should succeed
	if err := vm.ResizeVolume(context.Background(), volume.ID, remainingSectors); err != nil {
		t.Fatal(err)
	}

	// validate that the sectors were moved to the beginning of the volume
	for i, root := range roots {
		loc, release, err := db.SectorLocation(root)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if loc.Volume != volume.ID {
			t.Fatal(err)
		} else if loc.Index != uint64(i) {
			t.Fatalf("expected sector %v to be at index %v, got %v", root, i, loc.Index)
		} else if err := release(); err != nil {
			t.Fatal(err)
		}
	}
}
