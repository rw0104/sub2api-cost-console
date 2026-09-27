// Copyright 2026 Sub2API Cost Console contributors
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Windows can return S_FALSE with a usable overflow-area rectangle. HRESULT
// success is non-negative; failed calls or empty output must not supply bounds.
pub(crate) fn has_usable_rect(
    result: i32,
    (left, top, right, bottom): (i32, i32, i32, i32),
) -> bool {
    result >= 0 && right > left && bottom > top
}

#[cfg(test)]
mod tests {
    use super::has_usable_rect;

    #[test]
    fn accepts_s_ok_and_s_false_with_valid_rectangles() {
        for result in [0, 1] {
            assert!(has_usable_rect(result, (3441, 2088, 3477, 2160)));
        }
    }

    #[test]
    fn accepts_negative_screen_coordinates() {
        assert!(has_usable_rect(1, (-1920, -1080, -1884, -1008)));
    }

    #[test]
    fn rejects_failed_hresult_even_with_valid_output() {
        for result in [-1, i32::MIN, 0x80004005_u32 as i32] {
            assert!(!has_usable_rect(result, (3441, 2088, 3477, 2160)));
        }
    }

    #[test]
    fn rejects_empty_or_inverted_rectangles_even_on_success() {
        for result in [0, 1] {
            for rect in [
                (0, 0, 0, 0),
                (10, 20, 10, 30),
                (10, 20, 30, 20),
                (30, 20, 10, 40),
                (10, 40, 30, 20),
            ] {
                assert!(!has_usable_rect(result, rect));
            }
        }
    }
}
