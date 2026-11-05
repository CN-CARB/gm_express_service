-- Control-flow probe against the real addon, with GMod APIs mocked.
-- Usage: luajit net_lfs_optional_size.lua /path/to/gm_net_lfs/lua/net_lfs/sh_main.lua
local source = assert(arg[1], "pass the actual NetLFS sh_main.lua path")
local payload = string.rep("x", 70000)

local function environment()
    local state = {Requests = {}, Files = {}, Handlers = {}, Received = 0, Errors = {}}
    local env = setmetatable({}, {__index = _G})
    env.SERVER = false
    env.CLIENT = true
    env.Format = string.format
    env.NetLFS = {Cache = {Token_Client = "test-token"}, EndPoint = "http://test/v1"}
    env.hook = {Add = function() end}
    env.file = {
        CreateDir = function() end,
        Find = function() return {}, {} end,
        Read = function(path) return state.Files[path] end,
        Write = function(path, body) state.Files[path] = body end
    }
    env.util = {
        SHA256 = function(body) return body == payload and "good-hash" or "bad-hash" end,
        JSONToTable = function(body)
            return {size = body:match('"size"%s*:%s*"(%d+)"')}
        end
    }
    env.http = {Fetch = function(url, success, failure)
        state.Requests[#state.Requests + 1] = {URL = url, Success = success, Failure = failure}
    end}
    env.RiceLib = {
        Error = function(message) state.Errors[#state.Errors + 1] = message end,
        Protobuf = {
            Receive = function(name, callback) state.Handlers[name] = callback end,
            Send = function() end
        }
    }
    setfenv(assert(loadfile(source)), env)()
    state.Notify = function()
        state.Handlers["NetLFS.File.Receive"]({Receiver = "test", FileID = "file", FileHash = "good-hash", AckID = 0})
    end
    state.Receive = function(body)
        assert(body == payload)
        state.Received = state.Received + 1
    end

    return env, state
end

local env, state = environment()
env.NetLFS.RegisterReceiver("test", state.Receive)
state.Notify()
assert(#state.Requests == 1 and state.Requests[1].URL:find("/read/", 1, true))
state.Requests[1].Success(payload, #payload, {}, 200)
assert(state.Received == 1 and #state.Errors == 0)
state.Notify()
assert(state.Received == 2 and #state.Requests == 1)
print("PASS: nil filter skips size; repeat receipt uses verified disk cache without HTTP")

for _, allowed in ipairs({true, false}) do
    env, state = environment()
    local decisions = 0
    env.NetLFS.RegisterReceiver("test", state.Receive, function(id, hash, player, size)
        assert(id == "file" and hash == "good-hash" and size == #payload)
        decisions = decisions + 1
        return allowed
    end)
    state.Notify()
    assert(#state.Requests == 1 and state.Requests[1].URL:find("/size/", 1, true))
    state.Requests[1].Success('{"size":"70000"}', 16, {}, 200)
    assert(decisions == 1)
    if allowed then
        assert(#state.Requests == 2 and state.Requests[2].URL:find("/read/", 1, true))
        state.Requests[2].Success(payload, #payload, {}, 200)
        assert(state.Received == 1)
    else
        assert(#state.Requests == 1 and state.Received == 0)
    end
    print("PASS: custom filter gets size; allowed=" .. tostring(allowed))
end

env, state = environment()
env.NetLFS.RegisterReceiver("test", state.Receive, function() error("missing size must not reach filter") end)
state.Notify()
state.Requests[1].Success("{}", 2, {}, 200)
assert(#state.Requests == 1 and state.Received == 0 and #state.Errors == 1)
print("PASS: missing size stops before download")
