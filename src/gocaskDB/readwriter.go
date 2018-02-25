package gocaskDB

import (
	"os"
	"io/ioutil"
	"encoding/json"
	"io"
	"path"
	"strings"
	"encoding/binary"
	"util"
	"strconv"
)

type DBinfo struct {
	Dbname string
	Serial []int32
	Active int32
}

func getName(no int32, db *DB) string {
	return db.dbPath+"/"+db.dbinfo.Dbname+"_"+strconv.Itoa(int(no))
}

// Open all relative file by name of info file.
// If db doesn't exist, a new one will be created.
func OpenAllFile(filename string, db *DB) error {

	// open info file
	fInfo, info, err := OpenResolveInfoFile(filename)
	if err != nil {
		// If info file doesn't exist, create db.
		if err = CreateDBFiles(filename, db); err != nil {
			return err
		}
		return nil
	}
	db.infoFile = fInfo
	db.dbinfo = info

	// open active db file
	adb := getName(db.dbinfo.Active, db)+".gcdb"
	fAct, err := os.OpenFile(adb, os.O_WRONLY|os.O_APPEND, 0755)
	db.activeDBFile = fAct

	// open active hint file
	aht := getName(db.dbinfo.Active, db)+".gch"
	fActHint, err := os.OpenFile(aht, os.O_WRONLY|os.O_APPEND, 0755)
	db.activeHintFile = fActHint

	// open all db files to be read
	//rfiles := make([]string, 0)
	//for i := range db.dbinfo.Serial{
	//	rfiles = append(rfiles, getName(db.dbinfo.Serial[i], db)+".gcdb")
	//}
	db.dbFiles, err = OpenAllReadDBFiles(db.dbinfo.Serial, db)
	return nil
}

// Called while a new db is creating.
func CreateDBFiles(filename string, db *DB) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	info := new(DBinfo)
	info.Dbname = strings.TrimSuffix(path.Base(filename), path.Ext(filename))
	info.Active = 0;	// func NewActFiles will add 1 to Active
	info.Serial = make([]int32, 0)
	db.dbinfo = info
	db.infoFile = f
	db.dealingLock.Lock()
	if err = NewActFiles(db); err != nil {
		return err
	}
	if err = WriteInfoFile(f, info); err != nil {
		return err
	}
	return nil
}

func OpenResolveInfoFile(filename string) (*os.File, *DBinfo, error) {
	f, err := os.OpenFile(filename, os.O_RDWR, 0755)
	if err != nil {
		return nil, nil, err
	}
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	info := new(DBinfo)
	err = json.Unmarshal(bytes, info)
	if err != nil {
		return nil, nil, err
	}
	return f, info, nil;
}

func WriteInfoFile(file *os.File, info *DBinfo) error {
	bytes, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}
	err = file.Truncate(0)
	if err != nil {
		return err
	}
	_, err = file.Write(bytes)
	if err != nil {
		return err
	}
	return nil
}

// Open all db files to read from.
func OpenAllReadDBFiles(index []int32, db *DB) (map[int32]*os.File, error)  {
	fresult := make(map[int32]*os.File)
	for i := range index{
		if f, err := os.OpenFile(getName(index[i], db)+".gcdb", os.O_RDONLY, 0755); err != nil {
			return nil, err
		} else {
			fresult[index[i]] = f;
		}
	}
	return fresult, nil
}

func WriteData(data *DataPacket, db *DB) (body *hashBody, errr error) {
	databytes := data.getBytes()	//	crc	| tstmp	|  ksz	|  vsz	|  key	|  val
	var b1, b2 []byte

	// write db file
	// b1: record for .gcdb
	for i := range databytes {
		b1 = append(b1, databytes[i]...)
	}
	_, err := db.activeDBFile.Write(b1)
	if err != nil {
		return nil, err
	}

	// write hint file
	// I haven't found a function in go like ftell() in c, so Size is used here instead...
	info, err := db.activeDBFile.Stat();
	if err != nil {
		return nil, err
	}
	vpos := int32(info.Size()) - int32(binary.LittleEndian.Uint32(databytes[3])) // valpos = size(or file pointer position) - valsize
	b2 = append(b2, databytes[1]...)	// tstmp
	b2 = append(b2, databytes[2]...)	// ksz
	b2 = append(b2, databytes[3]...)	// vsz
	b2 = append(b2, util.ToBytes(vpos)...)	// vpos
	b2 = append(b2, databytes[4]...)	// key
	_, err = db.activeHintFile.Write(b2)
	if err != nil {
		return nil, err
	}

	// Set hash body.
	hbody := new(hashBody)
	hbody.vpos = vpos
	hbody.vsz = data.vsz
	hbody.timestamp = data.timestamp
	hbody.file = db.dbFiles[db.dbinfo.Active]


	/* 	If the size of active DB file >= file_max,
	 *	turn to new active Hint and DB files.
	*/
	stat, err := os.Stat(db.activeDBFile.Name())
	if err != nil {
		return nil, err
	}
		//fmt.Println(db.activeDBFile.Name())
		//fmt.Println(stat.Size())
	if stat.Size() >= int64(db.options.file_max) {
		// Lock in case that a write goroutine start before the new files are created.
		db.dealingLock.Lock()
		go func() {
			err := NewActFiles(db)
			if err != nil {
				panic(err) // halt the program
			}
		}()
	}
	return hbody, nil
}

// Create or update act db files (.gcdb, .gch), update read list (dbFiles) too.
func NewActFiles(db *DB) error {
	defer db.dealingLock.Unlock()
	if db.activeDBFile != nil {
		db.activeDBFile.Close()
	}
	if db.activeHintFile != nil {
		db.activeHintFile.Close()
	}
	filename := getName(db.dbinfo.Active + 1, db)
	// new db file
	if adb, err := os.Create(filename+".gcdb"); err != nil {
		return err
	} else {
		db.activeDBFile = adb
	}
	// new hint file
	if aht, err := os.Create(filename+".gch"); err != nil {
		return err
	} else {
		db.activeHintFile = aht
	}
	// add db file into read list
	if rdb, err := os.OpenFile(filename+".gcdb", os.O_RDONLY, 0755); err != nil {
		return err
	} else {
		db.dbFiles[db.dbinfo.Active+1] = rdb
	}
	// update db info
	db.dbinfo.Active += 1
	db.dbinfo.Serial = append(db.dbinfo.Serial, db.dbinfo.Active)
	WriteInfoFile(db.infoFile, db.dbinfo)
	//fmt.Println(db.dbFiles)
	return nil
}

func ReadValueFromFile(file *os.File, vpos int32, vsz int32) (Value, error) {
	b := make([]byte, vsz)
	_, err := file.ReadAt(b, int64(vpos))
	if err != nil {
		return Value(0), err
	}
	return Value(b), nil
}

func ReadRecordFromFile(file *os.File, vpos int32, ksz int32, vsz int32) (*DataPacket, error) {
	b := make([]byte, 20+ksz+vsz)
	_, err := file.ReadAt(b, int64(vpos-ksz-20))
	if err != nil {
		return nil, err
	}
	return bytesToData(b), err
}