package main

import (
	"fmt"

	"github.com/skeema/tengo"
)

func init() {
	long := `Updates the existing filesystem representation of the schemas and tables on a DB
instance. Use this command when changes have been applied to the database
without using skeema, and the filesystem representation needs to be updated to
reflect those changes.`

	Commands["pull"] = &Command{
		Name:    "pull",
		Short:   "Update the filesystem representation of schemas and tables",
		Long:    long,
		Options: nil,
		Handler: PullCommand,
	}
}

func PullCommand(cfg *Config) int {
	return pull(cfg, make(map[string]bool))
}

func pull(cfg *Config, seen map[string]bool) int {
	if cfg.Dir.IsLeaf() {
		fmt.Printf("Updating %s...\n", cfg.Dir.Path)

		t := cfg.Targets()[0]
		to := t.Schema(t.SchemaNames[0])
		if to == nil {
			if err := cfg.Dir.Delete(); err != nil {
				fmt.Printf("Unable to delete directory %s: %s\n", cfg.Dir, err)
				return 1
			}
			fmt.Printf("    Deleted directory %s -- schema no longer exists\n", cfg.Dir)
			return 0
		}

		if err := cfg.PopulateTemporarySchema(); err != nil {
			fmt.Printf("Unable to populate temporary schema: %s\n", err)
			return 1
		}

		from := t.TemporarySchema()
		diff := tengo.NewSchemaDiff(from, to)

		for _, td := range diff.TableDiffs {
			switch td := td.(type) {
			case tengo.CreateTable:
				table := td.Table
				createStmt, err := t.ShowCreateTable(to, table)
				if err != nil {
					panic(err)
				}
				sf := SQLFile{
					Dir:      cfg.Dir,
					FileName: fmt.Sprintf("%s.sql", table.Name),
					Contents: createStmt,
				}
				if length, err := sf.Write(); err != nil {
					fmt.Printf("Unable to write to %s: %s\n", sf.Path(), err)
					return 1
				} else {
					fmt.Printf("    Wrote %s (%d bytes) -- new table\n", sf.Path(), length)
				}
			case tengo.DropTable:
				table := td.Table
				sf := SQLFile{
					Dir:      cfg.Dir,
					FileName: fmt.Sprintf("%s.sql", table.Name),
				}
				if err := sf.Delete(); err != nil {
					fmt.Printf("Unable to delete %s: %s\n", sf.Path(), err)
					return 1
				}
				fmt.Printf("    Deleted %s -- table no longer exists\n", sf.Path())
			case tengo.AlterTable:
				table := td.Table
				createStmt, err := t.ShowCreateTable(to, table)
				if err != nil {
					panic(err)
				}
				sf := SQLFile{
					Dir:      cfg.Dir,
					FileName: fmt.Sprintf("%s.sql", table.Name),
					Contents: createStmt,
				}
				if length, err := sf.Write(); err != nil {
					fmt.Printf("Unable to write to %s: %s\n", sf.Path(), err)
					return 1
				} else {
					fmt.Printf("    Wrote %s (%d bytes) -- updated file to reflect table alterations\n", sf.Path(), length)
				}
			case tengo.RenameTable:
				panic(fmt.Errorf("Table renames not yet supported!"))
			default:
				panic(fmt.Errorf("Unsupported diff type %T\n", td))
			}
		}

		// TODO: also support a "normalize" option, which causes filesystem to reflect
		// format of SHOW CREATE TABLE

		if err := cfg.DropTemporarySchema(); err != nil {
			fmt.Printf("Unable to clean up temporary schema: %s\n", err)
			return 1
		}

	} else {
		subdirs, err := cfg.Dir.Subdirs()
		if err != nil {
			fmt.Printf("Unable to list subdirs of %s: %s\n", cfg.Dir, err)
			return 1
		}

		// If this dir represents an instance and its subdirs represent individual
		// schemas, iterate over them and track what schema names we see. Then
		// compare that to the schema list of the instance represented by the dir,
		// to see if there are any new schemas on the instance that need to be
		// created on the filesystem.
		if cfg.Dir.IsInstanceWithLeafSubdirs() {
			seenSchema := make(map[string]bool, len(subdirs))
			for _, subdir := range subdirs {
				skf, err := subdir.SkeemaFile(cfg)
				if err == nil && skf.HasField("schema") {
					seenSchema[skf.Values["schema"]] = true
				}
			}
			t := cfg.Targets()[0]
			for _, schema := range t.Schemas() {
				if !seenSchema[schema.Name] {
					// use same logic from init command
					ret := PopulateSchemaDir(schema, t.Instance, cfg.Dir, true)
					if ret != 0 {
						return ret
					}
				}
			}
		}

		// Finally, recurse into subdirs, avoiding duplication due to symlinks
		seen[cfg.Dir.Path] = true
		for n := range subdirs {
			subdir := subdirs[n]
			if !seen[subdir.Path] {
				ret := pull(cfg.ChangeDir(&subdir), seen)
				if ret != 0 {
					return ret
				}
			}
		}
	}

	return 0
}