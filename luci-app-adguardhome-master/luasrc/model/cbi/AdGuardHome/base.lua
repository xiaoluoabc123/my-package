require("luci.sys")
require("luci.util")
require("io")
local m,s,o
local fs=require"nixio.fs"
local uci=require"luci.model.uci".cursor()
local configpath=uci:get("AdGuardHome","AdGuardHome","configpath") or "/etc/AdGuardHome.yaml"
local binpath=uci:get("AdGuardHome","AdGuardHome","binpath") or "/usr/bin/AdGuardHome/AdGuardHome"
httpport=uci:get("AdGuardHome","AdGuardHome","httpport") or "3000"
m = Map("AdGuardHome", "AdGuard Home")
m.description = translate("Free and open source, powerful network-wide ads & trackers blocking DNS server.")
m:section(SimpleSection).template  = "AdGuardHome/AdGuardHome_status"

s = m:section(TypedSection, "AdGuardHome")
s.anonymous=true
s.addremove=false
---- enable
o = s:option(Flag, "enabled", translate("Enable"))
o.default = 0
o.optional = false
---- httpport
o =s:option(Value,"httpport",translate("Browser management port"))
o.placeholder=3000
o.default=3000
o.datatype="port"
o.optional = false
o.description = translate("<input type=\"button\" style=\"width:210px;border-color:Teal; text-align:center;font-weight:bold;color:Green;\" value=\"AdGuardHome Web:"..httpport.."\" onclick=\"window.open('http://'+window.location.hostname+':"..httpport.."/')\"/>")
---- update warning not safe
local binmtime=uci:get("AdGuardHome","AdGuardHome","binmtime") or "0"
local e=""
if not fs.access(configpath) then
	e=e.." no config"
end
if not fs.access(binpath) then
	e=e.." no bin"
else
	local version
	local testtime=fs.stat(binpath,"mtime")
	if testtime~=binmtime then
		local tmp=luci.sys.exec("touch /var/run/AdGfakeconfig;"..binpath.." -c /var/run/AdGfakeconfig --check-config 2>&1| grep -m 1 -E 'v[0-9.]+' -o ;rm /var/run/AdGfakeconfig")
		version=string.sub(tmp, 1, -2)
		uci:set("AdGuardHome","AdGuardHome","version",version)
		uci:set("AdGuardHome","AdGuardHome","binmtime",testtime)
		uci:save("AdGuardHome")
		uci:commit("AdGuardHome")
	else
		version=uci:get("AdGuardHome","AdGuardHome","version")
	end
	e=version..e
end
o=s:option(Button,"restart",translate("Update"))
o.inputtitle=translate("Update core version")
o.template = "AdGuardHome/AdGuardHome_check"
o.showfastconfig=(not fs.access(configpath))
o.description=string.format(translate("core version:").."<strong><font id=\"updateversion\" color=\"green\">%s </font></strong>",e)
---- port warning not safe
local port=luci.sys.exec("awk '/  port:/{printf($2);exit;}' "..configpath.." 2>nul")
if (port=="") then port="?" end
---- Redirect
o = s:option(ListValue, "redirect", port..translate("Redirect"), translate("AdGuardHome redirect mode"))
o.placeholder = "none"
o:value("none", translate("none"))
o:value("dnsmasq-upstream", translate("Run as dnsmasq upstream server"))
o:value("redirect", translate("Redirect 53 port to AdGuardHome"))
o:value("exchange", translate("Use port 53 replace dnsmasq"))
o.default     = "none"
o.optional = true
---- bin path
o = s:option(Value, "binpath", translate("Bin Path"), translate("AdGuardHome Bin path if no bin will auto download"))
o.default     = "/usr/bin/AdGuardHome/AdGuardHome"
o.datatype    = "string"
o.optional = false
o.validate=function(self, value)
if fs.stat(value,"type")=="dir" then
	fs.rmdir(value)
end
if fs.stat(value,"type")=="dir" then
	if (m.message) then
	m.message =m.message.."\nerror!bin path is a dir"
	else
	m.message ="error!bin path is a dir"
	end
	return nil
end 
return value
end
--- upx
o = s:option(ListValue, "upxflag", translate("use upx to compress bin after download"))
o:value("", translate("none"))
o:value("-1", translate("compress faster"))
o:value("-9", translate("compress better"))
o:value("--best", translate("compress best(can be slow for big files)"))
o:value("--brute", translate("try all available compression methods & filters [slow]"))
o:value("--ultra-brute", translate("try even more compression variants [very slow]"))
o.default     = ""
o.description=translate("bin use less space,but may have compatibility issues")
o.rmempty = true
---- config path
o = s:option(Value, "configpath", translate("Config Path"), translate("AdGuardHome config path"))
o.default     = "/etc/AdGuardHome.yaml"
o.datatype    = "string"
o.optional = false
o.validate=function(self, value)
if fs.stat(value,"type")=="dir" then
	fs.rmdir(value)
end
if fs.stat(value,"type")=="dir" then
	if m.message then
	m.message =m.message.."\nerror!config path is a dir"
	else
	m.message ="error!config path is a dir"
	end
	return nil
end 
return value
end
---- work dir
o = s:option(Value, "workdir", translate("Work dir"), translate("AdGuardHome work dir include rules,audit log and database"))
o.default     = "/usr/bin/AdGuardHome"
o.datatype    = "string"
o.optional = false
o.validate=function(self, value)
if fs.stat(value,"type")=="reg" then
	if m.message then
	m.message =m.message.."\nerror!work dir is a file"
	else
	m.message ="error!work dir is a file"
	end
	return nil
end 
if string.sub(value, -1)=="/" then
	return string.sub(value, 1, -2)
else
	return value
end
end
---- log file
o = s:option(Value, "logfile", translate("Runtime log file"), translate("AdGuardHome runtime Log file if 'syslog': write to system log;if empty no log"))
o.datatype    = "string"
o.rmempty = true
o.validate=function(self, value)
if fs.stat(value,"type")=="dir" then
	fs.rmdir(value)
end
if fs.stat(value,"type")=="dir" then
	if m.message then
	m.message =m.message.."\nerror!log file is a dir"
	else
	m.message ="error!log file is a dir"
	end
	return nil
end 
return value
end
---- debug
o = s:option(Flag, "verbose", translate("Verbose log"))
o.default = 0
o.optional = true
---- gfwlist 
local a=luci.sys.call("grep -m 1 -q programadd "..configpath)
if (a==0) then
a="Added"
else
a="Not added"
end
o=s:option(Button,"gfwdel",translate("Del gfwlist"),translate(a))
o.optional = true
o.inputtitle=translate("Del")
o.write=function()
	luci.sys.exec("sh /usr/share/AdGuardHome/gfw2adg.sh del 2>&1")
	luci.http.redirect(luci.dispatcher.build_url("admin","services","AdGuardHome"))
end
o=s:option(Button,"gfwadd",translate("Add gfwlist"),translate(a))
o.optional = true
o.inputtitle=translate("Add")
o.write=function()
	luci.sys.exec("sh /usr/share/AdGuardHome/gfw2adg.sh 2>&1")
	luci.http.redirect(luci.dispatcher.build_url("admin","services","AdGuardHome"))
end
o = s:option(Value, "gfwupstream", translate("Gfwlist upstream dns server"), translate("Gfwlist domain upstream dns service")..translate(a))
o.default     = "tcp://208.67.220.220:5353"
o.datatype    = "string"
o.optional = true
---- chpass
o = s:option(Value, "hashpass", translate("Change browser management password"), translate("Press load culculate model and culculate finally save/apply"))
o.default     = ""
o.datatype    = "string"
o.template = "AdGuardHome/AdGuardHome_chpass"
o.optional = true
---- database protect
o = s:option(Flag, "keepdb", translate("Keep database when system upgrade"))
o.default = 0
o.optional = true
---- wait net on boot
o = s:option(Flag, "waitonboot", translate("Boot delay until network ok"))
o.default = 1
o.optional = true
---- backup workdir on shutdown
o = s:option(Flag, "backupwd", translate("Backup workdir when shutdown"))
o.default = 0
o.optional = true
o.description=translate("Will be restore when workdir/data is empty")
----backup workdir path
o = s:option(Value, "backupwdpath", translate("Backup workdir path"))
o.default     = "/usr/bin/AdGuardHome"
o.datatype    = "string"
o.optional = true
o.validate=function(self, value)
if fs.stat(value,"type")=="reg" then
	if m.message then
	m.message =m.message.."\nerror!backup dir is a file"
	else
	m.message ="error!backup dir is a file"
	end
	return nil
end 
if string.sub(value,-1)=="/" then
	return string.sub(value, 1, -2)
else
	return value
end
end
----autoupdate
o = s:option(Flag, "autoupdate", translate("Auto update core with crontab"))
o.default = 0
o.optional = true
----cutquerylog
o = s:option(Flag, "cutquerylog", translate("Auto tail querylog with crontab"))
o.default = 0
o.optional = true
----downloadpath
o = s:option(TextValue, "downloadlinks",translate("Download links for update"))
o.optional = false
o.rows = 4
o.wrap = "on"
o.size=111
o.cfgvalue = function(self, section)
	return fs.readfile("/usr/share/AdGuardHome/links.txt")
end
o.write = function(self, section, value)
	fs.writefile("/usr/share/AdGuardHome/links.txt", value:gsub("\r\n", "\n"))
end
fs.writefile("/var/run/lucilogpos","0")
return m