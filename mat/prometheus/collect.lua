#!/usr/bin/lua
require 'nixio'

function dump(o)
   if type(o) == 'table' then
      local s = '{ '
      for k,v in pairs(o) do
         if type(k) ~= 'number' then k = '"'..k..'"' end
         s = s .. '['..k..'] = ' .. dump(v) .. ','
      end
      return s .. '} '
   else
      return tostring(o)
   end
end
function collect(f, t, dir, exclude)
  idir = nixio.fs.dir(dir)
  if idir == nil then
    -- Raced, directory no longer exists.
    return
  end
  for name in nixio.fs.dir(dir) do
    local path = dir .. '/' .. name
    if nixio.fs.lstat(path, 'type') == 'reg' and not exclude[name] then
      local header = ''
      local file = io.open(path)
      -- May have raced, file no longer exists.
      if file ~= nil then
        local data = file:read(4096) or ''
        file:close()

        function write(file, field, value, width)
          if value:len() > width then
            error('field ' .. field .. ' too long: ' .. value .. ' length ' .. tostring(value:len()) .. ' > ' .. tostring(width))
          end
          header = header .. value .. string.rep('\0', width - value:len())
        end
        write(f, 'path', name, 100)
        write(f, 'mode', '0400', 8)
        write(f, 'uid', '0', 8)
        write(f, 'gid', '0', 8)
        write(f, 'size', string.format('%011o ', data:len()), 12)
        write(f, 'mtime', string.format('%011o ', os.time()), 12)
        write(f, 'checksum', '        ', 8)
        write(f, 'link', '0', 1)
        write(f, 'linked', '', 100)
        if header:len() ~= 257 then
          error('expected header len 100, got ' .. tostring(header:len()))
        end
        write(f, 'ustar', 'ustar\0', 6)
        write(f, 'ver', '00', 2)
        write(f, 'owner', '', 32)
        write(f, 'group', '', 32)
        write(f, 'devmaj', '', 8)
        write(f, 'devmin', '', 8)
        write(f, 'prefix', tostring(t)..dir, 155)
        write(f, 'padding', '', 512 - header:len())

        local cs = 0
        for i = 1, header:len() do
          cs = cs + header:byte(i)
        end
        function patch(s, start, new)
          return s:sub(1, start)..new..s:sub(start + new:len() + 1)
        end
        header = patch(header, 100+8+8+8+12+12, string.format('%06o', cs))

        if header:len() ~= 512 then
          error('expected header len 512, got ' .. tostring(header:len()))
        end
        f:write(header)

        f:write(data)
        if data:len() % 512 ~= 0 then
          f:write(string.rep('\0', 512 - data:len() % 512))
        end
      end
    end
  end
end

function cycle(f, t)
  local dir = '/sys/kernel/debug/ieee80211'
  for phy in nixio.fs.dir(dir) do
    local phydir = dir..'/'..phy
    collect(f, t, phydir..'/statistics', {})
    collect(f, t, phydir..'/ath9k', {regdump=true})

    for netdev in nixio.fs.dir(phydir) do
      if string.find(netdev, '^netdev:') then
        local devdir = phydir..'/'..netdev
        collect(f, t, devdir, {})
        local dir = nixio.fs.dir(devdir..'/stations')
        -- May have raced, dir no longer exists
        if dir ~= nil then
          for sta in dir do
            local stadir = devdir..'/stations/'..sta
            collect(f, t, stadir, {rc_stats_csv=true, driver_buffered_tids=true})
          end
        end
      end
    end
  end
end

function gc()
  print('gc')
  local tars = {}
  for tar in nixio.fs.dir('/tmp/prom') do
    if string.find(tar, '.tar.gz$') then
      table.insert(tars, tar)
    end
  end
  table.sort(tars, function (t1, t2)
    return t1 > t2
  end)

  local size = 0
  for i, t in ipairs(tars) do
    size = size + nixio.fs.lstat('/tmp/prom/'..t, 'size')
    print('size: '..tostring(size))
    if size >= 10*1024*1024 then
      print('delete '..t)
      nixio.fs.remove('/tmp/prom/'..t)
    end
  end
end

function collectloop()
  local ts, tu = nixio.gettimeofday()
  while true do
    local t = string.format('%09d%06d', ts, tu)
    local zf = '/tmp/prom/'..t..'.tar.gz'
    print('open new file '..zf)
    io.open(zf, 'w'):close()
    local f = io.popen('exec /bin/gzip >'..zf, 'w')
    repeat
      t = string.format('%09d%06d', ts, tu)
      local buffer = {
        data = '',
        write = function (self, s)
          self.data = self.data..s
        end
      }
      cycle(buffer, t)
      f:write(buffer.data)
      f:flush()

      local full = nixio.fs.lstat(zf, 'size') >= 1024*1024
      if full then
        gc()
      end

      local ts1, tu1 = nixio.gettimeofday()
      local dt = (ts1 - ts) + (tu1 - tu) / 1000000.0
      print('collect time '..tostring(dt))
      local sleep = math.max(0.1, math.min(4.0, 2.0 - dt))
      print('sleep '..tostring(sleep))
      nixio.nanosleep(math.floor(sleep), math.floor((sleep % 1.0) * 1e9))

      ts, tu = nixio.gettimeofday()
    until full
    f:write(string.rep('\0', 512+512))
    f:close()
  end
end

collectloop()
