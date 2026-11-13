// voice-helper: a tiny stdio bridge to the Moonshine C API.
//
// The Electron main process owns this process. It writes framed commands
// to stdin and reads one JSON object per line from stdout:
//
//   frame: u8 type, u32le payload length, payload
//     type 1 audio: float32le PCM at 16 kHz, mono
//     type 2 load:  u32le model arch, then the model directory (UTF-8)
//     type 3 stop:  flush the last audio and finish the lines
//     type 4 cancel: drop the active stream
//     type 5 shutdown: exit
//
//   stdout events:
//     {"event":"ready","arch":2,"dir":"..."}
//     {"event":"line","index":0,"id":"...","text":"...","complete":false,"latencyMs":71}
//     {"event":"stopped"}
//     {"event":"cancelled"}
//     {"event":"error","message":"..."}
//
// Lines are stable: the helper re-sends the last incomplete line when it
// changes, and a completed line never changes again (the C API guarantee).
// All model work stays on this process's thread, so the UI process never
// blocks on inference.

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

#include "moonshine-c-api.h"

#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#include <windows.h>
#else
#include <sys/resource.h>
#endif

namespace {

enum Frame : uint8_t {
  FrameAudio = 1,
  FrameLoad = 2,
  FrameStop = 3,
  FrameCancel = 4,
  FrameShutdown = 5,
};

int32_t transcriber = -1;
int32_t stream = -1;
uint32_t loaded_arch = 0;
std::string loaded_dir;
uint64_t sent_lines = 0;

bool read_exact(void *buf, size_t n) {
  return std::fread(buf, 1, n, stdin) == n;
}

void write_json_string(std::string &out, const char *text) {
  out.push_back('"');
  if (text) {
    for (const unsigned char *p = reinterpret_cast<const unsigned char *>(text); *p; ++p) {
      switch (*p) {
        case '"': out += "\\\""; break;
        case '\\': out += "\\\\"; break;
        case '\n': out += "\\n"; break;
        case '\r': out += "\\r"; break;
        case '\t': out += "\\t"; break;
        default:
          if (*p < 0x20) {
            char escaped[7];
            std::snprintf(escaped, sizeof(escaped), "\\u%04x", *p);
            out += escaped;
          } else {
            out.push_back(static_cast<char>(*p));
          }
      }
    }
  }
  out.push_back('"');
}

void emit(const std::string &line) {
  std::fwrite(line.data(), 1, line.size(), stdout);
  std::fputc('\n', stdout);
  std::fflush(stdout);
}

void emit_error(const std::string &message) {
  std::string out = "{\"event\":\"error\",\"message\":";
  write_json_string(out, message.c_str());
  out += "}";
  emit(out);
}

void emit_line(uint64_t index, const transcript_line_t &line) {
  std::string out = "{\"event\":\"line\",\"index\":";
  out += std::to_string(index);
  out += ",\"id\":\"";
  out += std::to_string(line.id);
  out += "\",\"text\":";
  write_json_string(out, line.text ? line.text : "");
  out += line.is_complete ? ",\"complete\":true" : ",\"complete\":false";
  if (line.last_transcription_latency_ms) {
    out += ",\"latencyMs\":";
    out += std::to_string(line.last_transcription_latency_ms);
  }
  out += "}";
  emit(out);
}

void free_stream() {
  if (transcriber >= 0 && stream >= 0) {
    moonshine_free_stream(transcriber, stream);
  }
  stream = -1;
  sent_lines = 0;
}

void unload() {
  free_stream();
  if (transcriber >= 0) {
    moonshine_free_transcriber(transcriber);
  }
  transcriber = -1;
  loaded_arch = 0;
  loaded_dir.clear();
}

// transcribe emits every new or changed line. The last line of a stream
// may still be in progress, so it is re-sent on each update; completed
// lines are never touched again.
void transcribe(bool force) {
  if (transcriber < 0 || stream < 0) {
    return;
  }
  transcript_t *transcript = nullptr;
  uint32_t flags = force ? MOONSHINE_FLAG_FORCE_UPDATE : 0;
  int32_t rc = moonshine_transcribe_stream(transcriber, stream, flags, &transcript);
  if (rc != MOONSHINE_ERROR_NONE) {
    emit_error(moonshine_error_to_string(rc));
    return;
  }
  if (!transcript) {
    return;
  }
  uint64_t start = sent_lines;
  if (force) {
    // The final drain must re-send the line that was in progress when the
    // user released, along with any lines added since the previous call.
    if (start > 0) start -= 1;
  } else if (transcript->line_count > 0 && sent_lines == transcript->line_count &&
             !transcript->lines[transcript->line_count - 1].is_complete) {
    start = transcript->line_count - 1;
  }
  for (uint64_t i = start; i < transcript->line_count; i++) {
    const transcript_line_t &line = transcript->lines[i];
    // New lines always go out. A line already sent is only re-sent when
    // the library says its text changed, so a long pause in speech does
    // not flood the UI with identical updates.
    if (i < sent_lines && !line.is_updated && !force) {
      continue;
    }
    emit_line(i, line);
  }
  sent_lines = transcript->line_count;
}

bool load(const std::string &dir, uint32_t arch) {
  if (transcriber >= 0 && loaded_dir == dir && loaded_arch == arch) {
    // Reuse the warm model; only a fresh stream is needed.
  } else {
    unload();
    int32_t handle = moonshine_load_transcriber_from_files(dir.c_str(), arch, nullptr, 0, MOONSHINE_HEADER_VERSION);
    if (handle < 0) {
      emit_error(std::string("could not load the voice model: ") + moonshine_error_to_string(handle));
      return false;
    }
    transcriber = handle;
    loaded_dir = dir;
    loaded_arch = arch;
  }
  free_stream();
  int32_t created = moonshine_create_stream(transcriber, 0);
  if (created < 0) {
    emit_error(std::string("could not start a voice stream: ") + moonshine_error_to_string(created));
    return false;
  }
  stream = created;
  int32_t started = moonshine_start_stream(transcriber, stream);
  if (started != MOONSHINE_ERROR_NONE) {
    emit_error(std::string("could not start listening: ") + moonshine_error_to_string(started));
    free_stream();
    return false;
  }
  sent_lines = 0;
  std::string out = "{\"event\":\"ready\",\"arch\":";
  out += std::to_string(arch);
  out += ",\"dir\":";
  write_json_string(out, dir.c_str());
  out += "}";
  emit(out);
  return true;
}

void stop() {
  if (transcriber < 0 || stream < 0) {
    emit("{\"event\":\"stopped\"}");
    return;
  }
  int32_t rc = moonshine_stop_stream(transcriber, stream);
  if (rc != MOONSHINE_ERROR_NONE) {
    emit_error(moonshine_error_to_string(rc));
  }
  // Draining after stop emits the final, complete lines.
  transcribe(true);
  free_stream();
  emit("{\"event\":\"stopped\"}");
}

void cancel() {
  free_stream();
  emit("{\"event\":\"cancelled\"}");
}

void add_audio(const std::vector<uint8_t> &payload) {
  if (transcriber < 0 || stream < 0 || payload.size() < sizeof(float)) {
    return;
  }
  size_t count = payload.size() / sizeof(float);
  std::vector<float> samples(count);
  std::memcpy(samples.data(), payload.data(), count * sizeof(float));
  int32_t rc = moonshine_transcribe_add_audio_to_stream(transcriber, stream, samples.data(),
                                                        static_cast<uint64_t>(count), 16000, 0);
  if (rc != MOONSHINE_ERROR_NONE) {
    emit_error(moonshine_error_to_string(rc));
    return;
  }
  // The library caches anything shorter than ~200 ms, so calling after
  // every chunk costs almost nothing and yields low-latency updates.
  transcribe(false);
}

}  // namespace

int main() {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
  SetPriorityClass(GetCurrentProcess(), BELOW_NORMAL_PRIORITY_CLASS);
#else
  // Dictation should never outrank the UI or the agent's own work.
  setpriority(PRIO_PROCESS, 0, 10);
#endif

  for (;;) {
    uint8_t type = 0;
    uint32_t length = 0;
    if (!read_exact(&type, 1) || !read_exact(&length, 4)) {
      break;
    }
    std::vector<uint8_t> payload(length);
    if (length > 0 && !read_exact(payload.data(), length)) {
      break;
    }
    switch (type) {
      case FrameAudio:
        add_audio(payload);
        break;
      case FrameLoad: {
        if (payload.size() < 4) {
          emit_error("voice: malformed load frame");
          break;
        }
        uint32_t arch = 0;
        std::memcpy(&arch, payload.data(), 4);
        std::string dir(reinterpret_cast<const char *>(payload.data()) + 4, payload.size() - 4);
        load(dir, arch);
        break;
      }
      case FrameStop:
        stop();
        break;
      case FrameCancel:
        cancel();
        break;
      case FrameShutdown:
        unload();
        return 0;
      default:
        emit_error("voice: unknown frame");
        break;
    }
  }
  unload();
  return 0;
}
