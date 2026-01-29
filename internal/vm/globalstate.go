package vm

import (
	"database/sql"
	"fmt"
	"os"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	GlobalStateDBPath = "/tmp/homura_vm_state.db"
	MaxVMSlots        = 254
	BasePort          = 10000
	PortsPerSlot      = 10
)

// VMSlot represents an allocated VM slot with IP and port range
type VMSlot struct {
	SlotNumber         int
	IPAddress          string
	PortStart          int
	PortEnd            int
	VMPID              int
	PasstSocketPath    string

	// 9p fields
	WorkingDir         string
	NinePPID           int
	NinePControlSocket string
	NinePControlPort   int
}

// initGlobalStateDB initializes the global state database with proper schema
func initGlobalStateDB() error {
	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	// Set SQLite pragmas per user's preferences
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -6000",
		"PRAGMA foreign_keys = true",
		"PRAGMA temp_store = memory",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	// Create vm_slots table
	schema := `
	CREATE TABLE IF NOT EXISTS vm_slots (
		slot_number INTEGER PRIMARY KEY,
		ip_address TEXT NOT NULL UNIQUE,
		port_start INTEGER NOT NULL,
		port_end INTEGER NOT NULL,
		vm_pid INTEGER NOT NULL,
		passt_socket_path TEXT NOT NULL,
		working_dir TEXT NOT NULL,
		ninep_pid INTEGER NOT NULL,
		ninep_control_socket TEXT NOT NULL,
		ninep_control_port INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	) STRICT;

	CREATE INDEX IF NOT EXISTS idx_vm_pid ON vm_slots(vm_pid);
	CREATE INDEX IF NOT EXISTS idx_working_dir ON vm_slots(working_dir);
	CREATE INDEX IF NOT EXISTS idx_ninep_pid ON vm_slots(ninep_pid);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// isPIDAlive checks if a process with the given PID is still running
func isPIDAlive(pid int) bool {
	// Send signal 0 to check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// CleanupStaleSlots removes slots for dead PIDs
func CleanupStaleSlots() error {
	if err := initGlobalStateDB(); err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	// Get all slots
	rows, err := db.Query("SELECT slot_number, vm_pid FROM vm_slots")
	if err != nil {
		return fmt.Errorf("failed to query slots: %w", err)
	}
	defer rows.Close()

	var stalePIDs []int
	for rows.Next() {
		var slotNumber, vmPID int
		if err := rows.Scan(&slotNumber, &vmPID); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		if !isPIDAlive(vmPID) {
			stalePIDs = append(stalePIDs, slotNumber)
		}
	}

	// Delete stale slots
	if len(stalePIDs) > 0 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		stmt, err := tx.Prepare("DELETE FROM vm_slots WHERE slot_number = ?")
		if err != nil {
			return fmt.Errorf("failed to prepare delete statement: %w", err)
		}
		defer stmt.Close()

		for _, slotNum := range stalePIDs {
			if _, err := stmt.Exec(slotNum); err != nil {
				return fmt.Errorf("failed to delete stale slot %d: %w", slotNum, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit cleanup transaction: %w", err)
		}
	}

	return nil
}

// AllocateVMSlot finds and claims the next available slot
func AllocateVMSlot(socketPath, workingDir string, ninepPID int, ninepSocket string, ninepPort int) (*VMSlot, error) {
	if err := initGlobalStateDB(); err != nil {
		return nil, err
	}

	// Clean up stale slots first
	if err := CleanupStaleSlots(); err != nil {
		return nil, fmt.Errorf("failed to cleanup stale slots: %w", err)
	}

	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	// Use IMMEDIATE transaction to prevent race conditions
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("failed to begin immediate transaction: %w", err)
	}
	// Use defer with explicit rollback/commit handling
	committed := false
	defer func() {
		if !committed {
			db.Exec("ROLLBACK")
		}
	}()

	// Find next available slot (1-254)
	var nextSlot int
	for slot := 1; slot <= MaxVMSlots; slot++ {
		var exists int
		err := db.QueryRow("SELECT COUNT(*) FROM vm_slots WHERE slot_number = ?", slot).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("failed to check slot availability: %w", err)
		}

		if exists == 0 {
			nextSlot = slot
			break
		}
	}

	if nextSlot == 0 {
		return nil, fmt.Errorf("no available VM slots (maximum %d VMs running)", MaxVMSlots)
	}

	// Calculate IP and port range
	ipAddress := fmt.Sprintf("127.0.0.%d", nextSlot)
	portStart := BasePort + (nextSlot-1)*PortsPerSlot
	portEnd := portStart + PortsPerSlot - 1

	// Insert the slot
	vmPID := os.Getpid()
	createdAt := time.Now().UnixMilli()
	_, err = db.Exec(`
		INSERT INTO vm_slots (
			slot_number, ip_address, port_start, port_end, vm_pid,
			passt_socket_path, working_dir, ninep_pid, ninep_control_socket,
			ninep_control_port, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nextSlot, ipAddress, portStart, portEnd, vmPID,
	   socketPath, workingDir, ninepPID, ninepSocket, ninepPort, createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert slot: %w", err)
	}

	if _, err := db.Exec("COMMIT"); err != nil {
		return nil, fmt.Errorf("failed to commit slot allocation: %w", err)
	}
	committed = true

	return &VMSlot{
		SlotNumber:         nextSlot,
		IPAddress:          ipAddress,
		PortStart:          portStart,
		PortEnd:            portEnd,
		VMPID:              vmPID,
		PasstSocketPath:    socketPath,
		WorkingDir:         workingDir,
		NinePPID:           ninepPID,
		NinePControlSocket: ninepSocket,
		NinePControlPort:   ninepPort,
	}, nil
}

// ReleaseVMSlot frees a slot when the VM stops
func ReleaseVMSlot(slotNumber int) error {
	if err := initGlobalStateDB(); err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	_, err = db.Exec("DELETE FROM vm_slots WHERE slot_number = ?", slotNumber)
	if err != nil {
		return fmt.Errorf("failed to release slot %d: %w", slotNumber, err)
	}

	return nil
}

// FindVMByWorkDir finds a running VM by its working directory
func FindVMByWorkDir(workDir string) (*VMSlot, error) {
	if err := initGlobalStateDB(); err != nil {
		return nil, err
	}

	// Clean up stale slots first
	if err := CleanupStaleSlots(); err != nil {
		return nil, fmt.Errorf("failed to cleanup stale slots: %w", err)
	}

	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	var slot VMSlot
	err = db.QueryRow(`
		SELECT slot_number, ip_address, port_start, port_end, vm_pid,
		       passt_socket_path, working_dir, ninep_pid, ninep_control_socket,
		       ninep_control_port
		FROM vm_slots
		WHERE working_dir = ?
	`, workDir).Scan(
		&slot.SlotNumber, &slot.IPAddress, &slot.PortStart, &slot.PortEnd,
		&slot.VMPID, &slot.PasstSocketPath, &slot.WorkingDir,
		&slot.NinePPID, &slot.NinePControlSocket, &slot.NinePControlPort,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no VM found for working directory: %s", workDir)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find VM: %w", err)
	}

	return &slot, nil
}

// FindVMByNinePPID finds a running VM by its 9passthrough PID
func FindVMByNinePPID(ninepPID int) (*VMSlot, error) {
	if err := initGlobalStateDB(); err != nil {
		return nil, err
	}

	// Clean up stale slots first
	if err := CleanupStaleSlots(); err != nil {
		return nil, fmt.Errorf("failed to cleanup stale slots: %w", err)
	}

	db, err := sql.Open("sqlite3", GlobalStateDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open global state DB: %w", err)
	}
	defer db.Close()

	var slot VMSlot
	err = db.QueryRow(`
		SELECT slot_number, ip_address, port_start, port_end, vm_pid,
		       passt_socket_path, working_dir, ninep_pid, ninep_control_socket,
		       ninep_control_port
		FROM vm_slots
		WHERE ninep_pid = ?
	`, ninepPID).Scan(
		&slot.SlotNumber, &slot.IPAddress, &slot.PortStart, &slot.PortEnd,
		&slot.VMPID, &slot.PasstSocketPath, &slot.WorkingDir,
		&slot.NinePPID, &slot.NinePControlSocket, &slot.NinePControlPort,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no VM found for 9passthrough PID: %d", ninepPID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find VM: %w", err)
	}

	return &slot, nil
}
