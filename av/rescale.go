// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdint.h>
// #include <libavutil/mathematics.h>
//
// // mm_seconds_to_tb converts seconds to a stream timestamp. av_d2q turns the
// // double into a rational and av_rescale_q does the rounding, which is
// // AV_ROUND_NEAR_INF: nearest, halfway cases away from zero.
// static int64_t mm_seconds_to_tb(double seconds, int tb_num, int tb_den, int *ok) {
//     AVRational q;
//     int64_t ticks;
//     *ok = 0;
//     if (!(seconds >= 0.0) || tb_num <= 0 || tb_den <= 0)
//         return 0;
//     q = av_d2q(seconds, 1 << 26);
//     if (q.num < 0 || q.den <= 0)
//         return 0;
//     ticks = av_rescale_q(q.num, (AVRational){1, q.den}, (AVRational){tb_num, tb_den});
//     if (ticks == INT64_MIN)
//         return 0;
//     *ok = 1;
//     return ticks;
// }
import "C"

import "fmt"

// SecondsToTicks converts a non-negative number of seconds into ticks of the
// time base tbNum/tbDen. Rounding is av_rescale_q's: nearest, and a halfway
// case goes away from zero.
func SecondsToTicks(seconds float64, tbNum, tbDen int) (int64, error) {
	var ok C.int
	ticks := C.mm_seconds_to_tb(C.double(seconds), C.int(tbNum), C.int(tbDen), &ok)
	if ok == 0 {
		return 0, fmt.Errorf("av: cannot convert %v seconds in time base %d/%d", seconds, tbNum, tbDen)
	}
	return int64(ticks), nil
}
