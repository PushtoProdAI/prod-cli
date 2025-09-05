package analyzer

type LauncherFile struct {
	Name    string
	Content string
}

type LaunchContext struct {
	Launchers []LauncherFile
	Readme    string
}
