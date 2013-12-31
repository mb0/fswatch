fswatch
=======

fswatch handles file change notifications and caches file informations.

The included `Watcher` has the `Lstat` and `Walk` methods that mimic `os.Lstat` and `filepath.Walk` for cached file informations.


Usage
-----
Documentation can be found at http://godoc.org/github.com/mb0/fswatch

Basic example:

	func main() {
		watcher, err := fswatch.New(&fswatch.Context{
			Handle: func(event fswatch.Event, fi fswatch.FileInfo) {
				log.Println(event, fi.Name())
			},
			Filter: func(info fswatch.FileInfo) bool {
				n := info.Name()
				return len(n) != 0 && n[0] != '.' && n[len(n)-1] != '~'
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		defer watcher.Close()
		err = watcher.Load(runtime.GOROOT(), true)
		if err != nil {
			log.Fatal(err)
		}
		c := make(chan os.Signal)
		signal.Notify(c, os.Interrupt, os.Kill)
		<-c
	}

License
-------
fswatch is BSD licensed, Copyright (c) 2013 Martin Schnabel

fswatch was written from scratch with some inspiration from github.com/howeyc/fsnotify
