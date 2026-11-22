#include "bridge.h"
#include "_cgo_export.h"
#include <util/platform.h>

enum feed_kind { CAMERA, SCREEN, AUDIO };

struct webcam_input {
	obs_source_t *source;
	uintptr_t worker;
	enum feed_kind kind;
};

uint64_t webcam_now(void)
{
	return os_gettime_ns();
}

void webcam_video(obs_source_t *source, uint8_t *pixels, uint32_t width, uint32_t height)
{
	if (!pixels) {
		obs_source_output_video(source, NULL);
		return;
	}
	struct obs_source_frame frame = {0};
	frame.data[0] = pixels;
	frame.linesize[0] = width * 4;
	frame.width = width;
	frame.height = height;
	frame.timestamp = webcam_now();
	frame.format = VIDEO_FORMAT_BGRX;
	frame.full_range = true;
	obs_source_output_video(source, &frame);
}

void webcam_audio(obs_source_t *source, float *samples, uint32_t frames,
		  uint32_t rate, uint32_t channels, uint64_t timestamp)
{
	struct obs_source_audio audio = {0};
	audio.data[0] = (uint8_t *)samples;
	audio.frames = frames;
	audio.samples_per_sec = rate;
	audio.speakers = channels == 1 ? SPEAKERS_MONO : SPEAKERS_STEREO;
	audio.format = AUDIO_FORMAT_FLOAT;
	audio.timestamp = timestamp;
	obs_source_output_audio(source, &audio);
}

static void update_input(void *data, obs_data_t *settings)
{
	struct webcam_input *input = data;
	webcam_stop(input->worker);
	if (input->kind != AUDIO) {
		webcam_video(input->source, NULL, 0, 0);
		obs_source_set_async_unbuffered(input->source, true);
	}
	input->worker = webcam_start(input->source, input->kind,
				     (char *)obs_data_get_string(settings, "host"),
				     (int)obs_data_get_int(settings, "port"),
				     (int)obs_data_get_int(settings, "fps_limit"),
				     (int)obs_data_get_int(settings, "decode_scale"));
}

static void *create_input(obs_data_t *settings, obs_source_t *source, enum feed_kind kind)
{
	struct webcam_input *input = bzalloc(sizeof(*input));
	input->source = source;
	input->kind = kind;
	update_input(input, settings);
	return input;
}

static void destroy_input(void *data)
{
	struct webcam_input *input = data;
	webcam_stop(input->worker);
	bfree(input);
}

static obs_properties_t *properties(bool video)
{
	obs_properties_t *props = obs_properties_create();
	obs_properties_set_flags(props, OBS_PROPERTIES_DEFER_UPDATE);
	obs_properties_add_text(props, "host", "Sender IP (empty = LAN discovery)", OBS_TEXT_DEFAULT);
	obs_properties_add_int(props, "port", "Sender UDP port", 1, 65535, 1);
	if (video) {
		obs_properties_add_int(props, "fps_limit", "Decode FPS limit (0 = unlimited)", 0, 120, 1);
		obs_property_t *scale = obs_properties_add_list(props, "decode_scale", "Decode scale",
							      OBS_COMBO_TYPE_LIST, OBS_COMBO_FORMAT_INT);
		obs_property_list_add_int(scale, "Full", 1);
		obs_property_list_add_int(scale, "1/2", 2);
		obs_property_list_add_int(scale, "1/4", 4);
	}
	return props;
}

static obs_properties_t *video_properties(void *data) { (void)data; return properties(true); }
static obs_properties_t *audio_properties(void *data) { (void)data; return properties(false); }

static void defaults(obs_data_t *settings, int port)
{
	obs_data_set_default_string(settings, "host", "");
	obs_data_set_default_int(settings, "port", port);
	obs_data_set_default_int(settings, "fps_limit", 0);
	obs_data_set_default_int(settings, "decode_scale", 1);
}

static const char *camera_name(void *data) { (void)data; return "WebcamGo Camera"; }
static const char *screen_name(void *data) { (void)data; return "WebcamGo Screen"; }
static const char *audio_name(void *data) { (void)data; return "WebcamGo Audio"; }
static void camera_defaults(obs_data_t *s) { defaults(s, 5001); }
static void screen_defaults(obs_data_t *s) { defaults(s, 5002); }
static void audio_defaults(obs_data_t *s) { defaults(s, 5003); }
static void *create_camera(obs_data_t *s, obs_source_t *src) { return create_input(s, src, CAMERA); }
static void *create_screen(obs_data_t *s, obs_source_t *src) { return create_input(s, src, SCREEN); }
static void *create_audio(obs_data_t *s, obs_source_t *src) { return create_input(s, src, AUDIO); }

static struct obs_source_info camera_source = {
	.id = "webcamgo_camera", .type = OBS_SOURCE_TYPE_INPUT, .output_flags = OBS_SOURCE_ASYNC_VIDEO,
	.get_name = camera_name, .create = create_camera, .destroy = destroy_input,
	.update = update_input, .get_defaults = camera_defaults, .get_properties = video_properties,
};
static struct obs_source_info screen_source = {
	.id = "webcamgo_screen", .type = OBS_SOURCE_TYPE_INPUT, .output_flags = OBS_SOURCE_ASYNC_VIDEO,
	.get_name = screen_name, .create = create_screen, .destroy = destroy_input,
	.update = update_input, .get_defaults = screen_defaults, .get_properties = video_properties,
};
static struct obs_source_info audio_source = {
	.id = "webcamgo_audio", .type = OBS_SOURCE_TYPE_INPUT, .output_flags = OBS_SOURCE_AUDIO,
	.get_name = audio_name, .create = create_audio, .destroy = destroy_input,
	.update = update_input, .get_defaults = audio_defaults, .get_properties = audio_properties,
};

bool webcam_module_load(void)
{
	obs_register_source(&camera_source);
	obs_register_source(&screen_source);
	obs_register_source(&audio_source);
	return true;
}

uint32_t webcam_module_version(void) { return LIBOBS_API_VER; }
