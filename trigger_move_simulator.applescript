repeat
	try
		tell application "System Events" to set applist to (name of every process whose background only = false) -- See which apps are running
	end try
	if applist contains "iPhone Simulator" then
		delay 0.5
		tell application "System Events" to keystroke "c" using {command down, option down, control down}
		tell application "iPhone Simulator" to activate
		exit repeat
	end if
	delay 0.5
end repeat