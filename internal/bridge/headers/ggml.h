/**
 * ggml.h — ABI reference for janus/internal/bridge (ggml types)
 *
 * This file is NOT compiled. It documents the ggml tensor types and
 * enum values used in llama_context_params (type_k, type_v fields).
 *
 * Source: llama.cpp / ggml (https://github.com/ggerganov/llama.cpp)
 * Used by: internal/bridge/llama_dl.go (LlamaContextParams.type_k/type_v)
 */

#pragma once
#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------------
 * ggml_type — quantisation and data-type enum
 *
 * These int32 values are used in LlamaContextParams.type_k / type_v to
 * control the KV-cache storage format (affects VRAM usage vs quality).
 *
 * Recommended values for Vulkan (RTX 5070 / AMD):
 *   type_k = GGML_TYPE_F16  (default, best quality)
 *   type_v = GGML_TYPE_F16  (default)
 *   type_k = GGML_TYPE_Q8_0 (saves ~50% KV-cache VRAM, negligible quality loss)
 * ---------------------------------------------------------------------- */

enum ggml_type {
    GGML_TYPE_F32     = 0,
    GGML_TYPE_F16     = 1,
    GGML_TYPE_Q4_0    = 2,
    GGML_TYPE_Q4_1    = 3,
    GGML_TYPE_Q5_0    = 6,
    GGML_TYPE_Q5_1    = 7,
    GGML_TYPE_Q8_0    = 8,
    GGML_TYPE_Q8_1    = 9,
    GGML_TYPE_Q2_K    = 10,
    GGML_TYPE_Q3_K    = 11,
    GGML_TYPE_Q4_K    = 12,
    GGML_TYPE_Q5_K    = 13,
    GGML_TYPE_Q6_K    = 14,
    GGML_TYPE_Q8_K    = 15,
    GGML_TYPE_IQ2_XXS = 16,
    GGML_TYPE_IQ2_XS  = 17,
    GGML_TYPE_IQ3_XXS = 18,
    GGML_TYPE_IQ1_S   = 19,
    GGML_TYPE_IQ4_NL  = 20,
    GGML_TYPE_IQ3_S   = 21,
    GGML_TYPE_IQ2_S   = 22,
    GGML_TYPE_IQ4_XS  = 23,
    GGML_TYPE_I8      = 24,
    GGML_TYPE_I16     = 25,
    GGML_TYPE_I32     = 26,
    GGML_TYPE_I64     = 27,
    GGML_TYPE_F64     = 28,
    GGML_TYPE_IQ1_M   = 29,
    GGML_TYPE_BF16    = 30,
    GGML_TYPE_COUNT,
};

/* -------------------------------------------------------------------------
 * ggml_tensor — layout reference (NOT used in bridge directly)
 *
 * Janus never touches ggml_tensor internals. This is here purely as a
 * reference in case you need to inspect tensor shapes during debugging.
 * ---------------------------------------------------------------------- */

#define GGML_MAX_DIMS 4

struct ggml_tensor {
    enum ggml_type type;
    int            n_dims;
    int64_t        ne[GGML_MAX_DIMS]; /* number of elements */
    size_t         nb[GGML_MAX_DIMS]; /* stride in bytes     */
    void         * op_params;
    int32_t        flags;
    struct ggml_tensor * grad;
    struct ggml_tensor * src[10];
    struct ggml_context * ctx;
    void         * data;
    char           name[64];
    void         * extra;
};

/* -------------------------------------------------------------------------
 * Backend scheduling callback type
 * Used in llama_context_params.cb_eval (set to NULL in default params)
 * ---------------------------------------------------------------------- */

struct ggml_tensor;
struct ggml_cplan;
typedef bool (*ggml_backend_sched_eval_callback)(
    struct ggml_tensor * t,
    bool                 ask,
    void               * user_data
);

#ifdef __cplusplus
}
#endif
