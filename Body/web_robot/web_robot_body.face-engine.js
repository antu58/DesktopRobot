(function (global) {
    function initRobotFacePanel() {
// 机器人表情：从 web_face_demo 精简整合（仅保留表情渲染核心）
        (function () {
            const STAGE_W = 1024;
            const STAGE_H = 512;
            const TARGET_FPS = 60;
            const EYES_FRAME_MS = 1000 / TARGET_FPS;
            const BASE_SCREEN_W = 128;
            const BASE_SCREEN_H = 64;
            const GEOMETRY_SCALE = Math.min(STAGE_W / BASE_SCREEN_W, STAGE_H / BASE_SCREEN_H);

            const EMOTIONS = [
                'Normal', 'Angry', 'Glee', 'Happy', 'Sad', 'Worried', 'Focused', 'Annoyed',
                'Surprised', 'Skeptic', 'Frustrated', 'Unimpressed', 'Sleepy', 'Suspicious',
                'Squint', 'Furious', 'Scared', 'Awe'
            ];

            const PRESETS = {
                Normal: { OffsetX: 0, OffsetY: 0, Height: 40, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 8, Radius_Bottom: 8, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Happy: { OffsetX: 0, OffsetY: 0, Height: 10, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 10, Radius_Bottom: 0, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Glee: { OffsetX: 0, OffsetY: 0, Height: 8, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 8, Radius_Bottom: 0, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 5, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Sad: { OffsetX: 0, OffsetY: 0, Height: 15, Width: 40, Slope_Top: -0.5, Slope_Bottom: 0, Radius_Top: 1, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Worried: { OffsetX: 0, OffsetY: 0, Height: 25, Width: 40, Slope_Top: -0.1, Slope_Bottom: 0, Radius_Top: 6, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Worried_Alt: { OffsetX: 0, OffsetY: 0, Height: 35, Width: 40, Slope_Top: -0.2, Slope_Bottom: 0, Radius_Top: 6, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Focused: { OffsetX: 0, OffsetY: 0, Height: 14, Width: 40, Slope_Top: 0.2, Slope_Bottom: 0, Radius_Top: 3, Radius_Bottom: 1, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Annoyed: { OffsetX: 0, OffsetY: 0, Height: 12, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 0, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Annoyed_Alt: { OffsetX: 0, OffsetY: 0, Height: 5, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 0, Radius_Bottom: 4, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Surprised: { OffsetX: -2, OffsetY: 0, Height: 45, Width: 45, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 16, Radius_Bottom: 16, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Skeptic: { OffsetX: 0, OffsetY: 0, Height: 40, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 10, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Skeptic_Alt: { OffsetX: 0, OffsetY: -6, Height: 26, Width: 40, Slope_Top: 0.3, Slope_Bottom: 0, Radius_Top: 1, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Frustrated: { OffsetX: 3, OffsetY: -5, Height: 12, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 0, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Unimpressed: { OffsetX: 3, OffsetY: 0, Height: 12, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 1, Radius_Bottom: 10, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Unimpressed_Alt: { OffsetX: 3, OffsetY: -3, Height: 22, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 1, Radius_Bottom: 16, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Sleepy: { OffsetX: 0, OffsetY: -2, Height: 14, Width: 40, Slope_Top: -0.5, Slope_Bottom: -0.5, Radius_Top: 3, Radius_Bottom: 3, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Sleepy_Alt: { OffsetX: 0, OffsetY: -2, Height: 8, Width: 40, Slope_Top: -0.5, Slope_Bottom: -0.5, Radius_Top: 3, Radius_Bottom: 3, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Suspicious: { OffsetX: 0, OffsetY: 0, Height: 22, Width: 40, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 8, Radius_Bottom: 3, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Suspicious_Alt: { OffsetX: 0, OffsetY: -3, Height: 16, Width: 40, Slope_Top: 0.2, Slope_Bottom: 0, Radius_Top: 6, Radius_Bottom: 3, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Squint: { OffsetX: -10, OffsetY: -3, Height: 35, Width: 35, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 8, Radius_Bottom: 8, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Squint_Alt: { OffsetX: 5, OffsetY: 0, Height: 20, Width: 20, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 5, Radius_Bottom: 5, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Angry: { OffsetX: -3, OffsetY: 0, Height: 20, Width: 40, Slope_Top: 0.3, Slope_Bottom: 0, Radius_Top: 2, Radius_Bottom: 12, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Furious: { OffsetX: -2, OffsetY: 0, Height: 30, Width: 40, Slope_Top: 0.4, Slope_Bottom: 0, Radius_Top: 2, Radius_Bottom: 8, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Scared: { OffsetX: -3, OffsetY: 0, Height: 40, Width: 40, Slope_Top: -0.1, Slope_Bottom: 0, Radius_Top: 12, Radius_Bottom: 8, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 },
                Awe: { OffsetX: 2, OffsetY: 0, Height: 35, Width: 45, Slope_Top: -0.1, Slope_Bottom: 0.1, Radius_Top: 12, Radius_Bottom: 12, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 }
            };

            function nowMs() { return performance.now(); }

            function cloneConfig(c) {
                return {
                    OffsetX: c.OffsetX,
                    OffsetY: c.OffsetY,
                    Height: c.Height,
                    Width: c.Width,
                    Slope_Top: c.Slope_Top,
                    Slope_Bottom: c.Slope_Bottom,
                    Radius_Top: c.Radius_Top,
                    Radius_Bottom: c.Radius_Bottom,
                    Inverse_Radius_Top: c.Inverse_Radius_Top,
                    Inverse_Radius_Bottom: c.Inverse_Radius_Bottom,
                    Inverse_Offset_Top: c.Inverse_Offset_Top,
                    Inverse_Offset_Bottom: c.Inverse_Offset_Bottom
                };
            }

            function scalePresetConfig(c, scale) {
                return {
                    OffsetX: c.OffsetX * scale,
                    OffsetY: c.OffsetY * scale,
                    Height: c.Height * scale,
                    Width: c.Width * scale,
                    Slope_Top: c.Slope_Top,
                    Slope_Bottom: c.Slope_Bottom,
                    Radius_Top: c.Radius_Top * scale,
                    Radius_Bottom: c.Radius_Bottom * scale,
                    Inverse_Radius_Top: c.Inverse_Radius_Top * scale,
                    Inverse_Radius_Bottom: c.Inverse_Radius_Bottom * scale,
                    Inverse_Offset_Top: c.Inverse_Offset_Top * scale,
                    Inverse_Offset_Bottom: c.Inverse_Offset_Bottom * scale
                };
            }

            class AsyncTimer {
                constructor(interval, onFinish = null) {
                    this.Interval = interval;
                    this.OnFinish = onFinish;
                    this._isActive = false;
                    this._isExpired = false;
                    this._startTime = nowMs();
                }

                start(t = nowMs()) {
                    this.reset(t);
                    this._isActive = true;
                }

                reset(t = nowMs()) { this._startTime = t; }

                update(t = nowMs()) {
                    if (!this._isActive) return false;
                    this._isExpired = false;
                    if (t - this._startTime >= this.Interval) {
                        this._isExpired = true;
                        if (this.OnFinish) this.OnFinish();
                        this.reset(t);
                    }
                    return this._isExpired;
                }

                isExpired() { return this._isExpired; }
            }

            class AnimationBase {
                constructor(interval) {
                    this.Interval = interval;
                    this.StarTime = nowMs();
                }

                restart(t = nowMs()) { this.StarTime = t; }

                getElapsed(t = nowMs()) { return t - this.StarTime; }

                getValue(t = nowMs()) { return this.calculate(this.getElapsed(t)); }

                calculate() { return 0; }
            }

            class RampAnimation extends AnimationBase {
                calculate(elapsed) {
                    if (elapsed < this.Interval) return elapsed / this.Interval;
                    return 1;
                }
            }

            class TrapeziumAnimation extends AnimationBase {
                constructor(t0, t1, t2) {
                    super(t0 + t1 + t2);
                    this._t0 = t0;
                    this._t1 = t1;
                    this._t2 = t2;
                }

                calculate(elapsed) {
                    if (elapsed > this.Interval) return 0;
                    if (elapsed < this._t0) return elapsed / this._t0;
                    if (elapsed < this._t0 + this._t1) return 1;
                    return 1 - (elapsed - this._t1 - this._t0) / this._t2;
                }
            }

            class TrapeziumPulseAnimation extends AnimationBase {
                constructor(t0, t1, t2, t3, t4) {
                    super(t0 + t1 + t2 + t3 + t4);
                    this._t0 = t0;
                    this._t1 = t1;
                    this._t2 = t2;
                    this._t3 = t3;
                    this._t4 = t4;
                }

                calculate(elapsedMs) {
                    const elapsed = this.Interval === 0 ? 0 : elapsedMs % this.Interval;
                    if (elapsed < this._t0) return 0;
                    if (elapsed < this._t0 + this._t1) return (elapsed - this._t0) / this._t1;
                    if (elapsed < this._t0 + this._t1 + this._t2) return 1;
                    if (elapsed < this._t0 + this._t1 + this._t2 + this._t3) {
                        return 1 - (elapsed - this._t2 - this._t1 - this._t0) / this._t3;
                    }
                    return 0;
                }

                setTriangle(t, delay) {
                    this._t0 = 0;
                    this._t1 = t / 2;
                    this._t2 = 0;
                    this._t3 = this._t1;
                    this._t4 = delay;
                    this.Interval = this._t0 + this._t1 + this._t2 + this._t3 + this._t4;
                }
            }

            class EyeTransition {
                constructor() {
                    this.Origin = null;
                    this.Destin = cloneConfig(PRESETS.Normal);
                    this.Animation = new RampAnimation(500);
                }

                update(t) {
                    this.apply(this.Animation.getValue(t));
                }

                apply(v) {
                    this.Origin.OffsetX = this.Origin.OffsetX * (1 - v) + this.Destin.OffsetX * v;
                    this.Origin.OffsetY = this.Origin.OffsetY * (1 - v) + this.Destin.OffsetY * v;
                    this.Origin.Height = this.Origin.Height * (1 - v) + this.Destin.Height * v;
                    this.Origin.Width = this.Origin.Width * (1 - v) + this.Destin.Width * v;
                    this.Origin.Slope_Top = this.Origin.Slope_Top * (1 - v) + this.Destin.Slope_Top * v;
                    this.Origin.Slope_Bottom = this.Origin.Slope_Bottom * (1 - v) + this.Destin.Slope_Bottom * v;
                    this.Origin.Radius_Top = this.Origin.Radius_Top * (1 - v) + this.Destin.Radius_Top * v;
                    this.Origin.Radius_Bottom = this.Origin.Radius_Bottom * (1 - v) + this.Destin.Radius_Bottom * v;
                    this.Origin.Inverse_Radius_Top = this.Origin.Inverse_Radius_Top * (1 - v) + this.Destin.Inverse_Radius_Top * v;
                    this.Origin.Inverse_Radius_Bottom = this.Origin.Inverse_Radius_Bottom * (1 - v) + this.Destin.Inverse_Radius_Bottom * v;
                    this.Origin.Inverse_Offset_Top = this.Origin.Inverse_Offset_Top * (1 - v) + this.Destin.Inverse_Offset_Top * v;
                    this.Origin.Inverse_Offset_Bottom = this.Origin.Inverse_Offset_Bottom * (1 - v) + this.Destin.Inverse_Offset_Bottom * v;
                }
            }

            class EyeTransformation {
                constructor() {
                    this.Input = null;
                    this.Output = cloneConfig(PRESETS.Normal);
                    this.Origin = { MoveX: 0, MoveY: 0, ScaleX: 1, ScaleY: 1 };
                    this.Current = { MoveX: 0, MoveY: 0, ScaleX: 1, ScaleY: 1 };
                    this.Destin = { MoveX: 0, MoveY: 0, ScaleX: 1, ScaleY: 1 };
                    this.Animation = new RampAnimation(200);
                }

                update(t) {
                    const v = this.Animation.getValue(t);
                    this.Current.MoveX = (this.Destin.MoveX - this.Origin.MoveX) * v + this.Origin.MoveX;
                    this.Current.MoveY = (this.Destin.MoveY - this.Origin.MoveY) * v + this.Origin.MoveY;
                    this.Current.ScaleX = (this.Destin.ScaleX - this.Origin.ScaleX) * v + this.Origin.ScaleX;
                    this.Current.ScaleY = (this.Destin.ScaleY - this.Origin.ScaleY) * v + this.Origin.ScaleY;
                    this.apply();
                }

                apply() {
                    this.Output.OffsetX = this.Input.OffsetX + this.Current.MoveX;
                    this.Output.OffsetY = this.Input.OffsetY - this.Current.MoveY;
                    this.Output.Width = this.Input.Width * this.Current.ScaleX;
                    this.Output.Height = this.Input.Height * this.Current.ScaleY;
                    this.Output.Slope_Top = this.Input.Slope_Top;
                    this.Output.Slope_Bottom = this.Input.Slope_Bottom;
                    this.Output.Radius_Top = this.Input.Radius_Top;
                    this.Output.Radius_Bottom = this.Input.Radius_Bottom;
                    this.Output.Inverse_Radius_Top = this.Input.Inverse_Radius_Top;
                    this.Output.Inverse_Radius_Bottom = this.Input.Inverse_Radius_Bottom;
                    this.Output.Inverse_Offset_Top = this.Input.Inverse_Offset_Top;
                    this.Output.Inverse_Offset_Bottom = this.Input.Inverse_Offset_Bottom;
                }

                setDestin(transformation) {
                    this.Origin.MoveX = this.Current.MoveX;
                    this.Origin.MoveY = this.Current.MoveY;
                    this.Origin.ScaleX = this.Current.ScaleX;
                    this.Origin.ScaleY = this.Current.ScaleY;
                    this.Destin.MoveX = transformation.MoveX;
                    this.Destin.MoveY = transformation.MoveY;
                    this.Destin.ScaleX = transformation.ScaleX;
                    this.Destin.ScaleY = transformation.ScaleY;
                }
            }

            class EyeVariation {
                constructor() {
                    this.Input = null;
                    this.Output = cloneConfig(PRESETS.Normal);
                    this.Animation = new TrapeziumPulseAnimation(0, 1000, 0, 1000, 0);
                    this.Values = cloneConfig({ OffsetX: 0, OffsetY: 0, Height: 0, Width: 0, Slope_Top: 0, Slope_Bottom: 0, Radius_Top: 0, Radius_Bottom: 0, Inverse_Radius_Top: 0, Inverse_Radius_Bottom: 0, Inverse_Offset_Top: 0, Inverse_Offset_Bottom: 0 });
                }

                clear() {
                    this.Values.OffsetX = 0;
                    this.Values.OffsetY = 0;
                    this.Values.Height = 0;
                    this.Values.Width = 0;
                    this.Values.Slope_Top = 0;
                    this.Values.Slope_Bottom = 0;
                    this.Values.Radius_Top = 0;
                    this.Values.Radius_Bottom = 0;
                    this.Values.Inverse_Radius_Top = 0;
                    this.Values.Inverse_Radius_Bottom = 0;
                    this.Values.Inverse_Offset_Top = 0;
                    this.Values.Inverse_Offset_Bottom = 0;
                }

                update(t) {
                    const v = this.Animation.getValue(t);
                    this.apply(2 * v - 1);
                }

                apply(v) {
                    this.Output.OffsetX = this.Input.OffsetX + this.Values.OffsetX * v;
                    this.Output.OffsetY = this.Input.OffsetY + this.Values.OffsetY * v;
                    this.Output.Height = this.Input.Height + this.Values.Height * v;
                    this.Output.Width = this.Input.Width + this.Values.Width * v;
                    this.Output.Slope_Top = this.Input.Slope_Top + this.Values.Slope_Top * v;
                    this.Output.Slope_Bottom = this.Input.Slope_Bottom + this.Values.Slope_Bottom * v;
                    this.Output.Radius_Top = this.Input.Radius_Top + this.Values.Radius_Top * v;
                    this.Output.Radius_Bottom = this.Input.Radius_Bottom + this.Values.Radius_Bottom * v;
                    this.Output.Inverse_Radius_Top = this.Input.Inverse_Radius_Top + this.Values.Inverse_Radius_Top * v;
                    this.Output.Inverse_Radius_Bottom = this.Input.Inverse_Radius_Bottom + this.Values.Inverse_Radius_Bottom * v;
                    this.Output.Inverse_Offset_Top = this.Input.Inverse_Offset_Top + this.Values.Inverse_Offset_Top * v;
                    this.Output.Inverse_Offset_Bottom = this.Input.Inverse_Offset_Bottom + this.Values.Inverse_Offset_Bottom * v;
                }
            }

            class EyeBlink {
                constructor() {
                    this.Input = null;
                    this.Output = cloneConfig(PRESETS.Normal);
                    this.Animation = new TrapeziumAnimation(40, 100, 40);
                    this.BlinkWidth = 60 * GEOMETRY_SCALE;
                    this.BlinkHeight = 2 * GEOMETRY_SCALE;
                }

                update(t) {
                    let v = this.Animation.getValue(t);
                    if (this.Animation.getElapsed(t) > this.Animation.Interval) v = 0;
                    this.apply(v * v);
                }

                apply(v) {
                    this.Output.OffsetX = this.Input.OffsetX;
                    this.Output.OffsetY = this.Input.OffsetY;
                    this.Output.Width = (this.BlinkWidth - this.Input.Width) * v + this.Input.Width;
                    this.Output.Height = (this.BlinkHeight - this.Input.Height) * v + this.Input.Height;
                    this.Output.Slope_Top = this.Input.Slope_Top * (1 - v);
                    this.Output.Slope_Bottom = this.Input.Slope_Bottom * (1 - v);
                    this.Output.Radius_Top = this.Input.Radius_Top * (1 - v);
                    this.Output.Radius_Bottom = this.Input.Radius_Bottom * (1 - v);
                    this.Output.Inverse_Radius_Top = this.Input.Inverse_Radius_Top * (1 - v);
                    this.Output.Inverse_Radius_Bottom = this.Input.Inverse_Radius_Bottom * (1 - v);
                    this.Output.Inverse_Offset_Top = this.Input.Inverse_Offset_Top * (1 - v);
                    this.Output.Inverse_Offset_Bottom = this.Input.Inverse_Offset_Bottom * (1 - v);
                }
            }

            class Eye {
                constructor(face) {
                    this._face = face;
                    this.IsMirrored = false;
                    this.Config = cloneConfig(scalePresetConfig(PRESETS.Normal, GEOMETRY_SCALE));
                    this.FinalConfig = this.Config;
                    this.Transition = new EyeTransition();
                    this.Transformation = new EyeTransformation();
                    this.Variation1 = new EyeVariation();
                    this.Variation2 = new EyeVariation();
                    this.BlinkTransformation = new EyeBlink();
                    this.chainOperators();

                    this.Variation1.Animation._t0 = 200;
                    this.Variation1.Animation._t1 = 200;
                    this.Variation1.Animation._t2 = 200;
                    this.Variation1.Animation._t3 = 200;
                    this.Variation1.Animation._t4 = 0;
                    this.Variation1.Animation.Interval = 800;

                    this.Variation2.Animation._t0 = 0;
                    this.Variation2.Animation._t1 = 200;
                    this.Variation2.Animation._t2 = 200;
                    this.Variation2.Animation._t3 = 200;
                    this.Variation2.Animation._t4 = 200;
                    this.Variation2.Animation.Interval = 800;
                }

                chainOperators() {
                    this.Transition.Origin = this.Config;
                    this.Transformation.Input = this.Config;
                    this.Variation1.Input = this.Transformation.Output;
                    this.Variation2.Input = this.Variation1.Output;
                    this.BlinkTransformation.Input = this.Variation2.Output;
                    this.FinalConfig = this.BlinkTransformation.Output;
                }

                update(t) {
                    this.Transition.update(t);
                    this.Transformation.update(t);
                    this.Variation1.update(t);
                    this.Variation2.update(t);
                    this.BlinkTransformation.update(t);
                }

                transitionTo(config, t = nowMs()) {
                    const scaled = scalePresetConfig(config, GEOMETRY_SCALE);
                    this.Transition.Destin.OffsetX = this.IsMirrored ? -scaled.OffsetX : scaled.OffsetX;
                    this.Transition.Destin.OffsetY = -scaled.OffsetY;
                    this.Transition.Destin.Height = scaled.Height;
                    this.Transition.Destin.Width = scaled.Width;
                    this.Transition.Destin.Slope_Top = this.IsMirrored ? scaled.Slope_Top : -scaled.Slope_Top;
                    this.Transition.Destin.Slope_Bottom = this.IsMirrored ? scaled.Slope_Bottom : -scaled.Slope_Bottom;
                    this.Transition.Destin.Radius_Top = scaled.Radius_Top;
                    this.Transition.Destin.Radius_Bottom = scaled.Radius_Bottom;
                    this.Transition.Destin.Inverse_Radius_Top = scaled.Inverse_Radius_Top;
                    this.Transition.Destin.Inverse_Radius_Bottom = scaled.Inverse_Radius_Bottom;
                    this.Transition.Animation.restart(t);
                }
            }

            class BlinkAssistant {
                constructor(face) {
                    this._face = face;
                    this.Timer = new AsyncTimer(3500);
                    this.Timer.start();
                }

                update(t) {
                    this.Timer.update(t);
                    if (this.Timer.isExpired()) this.blink(t);
                }

                blink(t = nowMs()) {
                    this._face.LeftEye.BlinkTransformation.Animation.restart(t);
                    this._face.RightEye.BlinkTransformation.Animation.restart(t);
                    this.Timer.reset(t);
                }
            }

            class LookAssistant {
                constructor(face) {
                    this._face = face;
                    this.Timer = new AsyncTimer(4000);
                    this.Timer.start();
                    this.transformation = { MoveX: 0, MoveY: 0, ScaleX: 1, ScaleY: 1 };
                }

                lookAt(x, y, t = nowMs()) {
                    const g = GEOMETRY_SCALE;
                    const moveXx = -25 * g * x;
                    const moveYy = 20 * g * y;
                    let scaleYx = 1 - x * 0.2;
                    const scaleYy = 1 - Math.abs(y) * 0.4;

                    this.transformation.MoveX = moveXx;
                    this.transformation.MoveY = moveYy;
                    this.transformation.ScaleX = 1;
                    this.transformation.ScaleY = scaleYx * scaleYy;
                    this._face.RightEye.Transformation.setDestin(this.transformation);

                    scaleYx = 1 + x * 0.2;
                    this.transformation.MoveX = moveXx;
                    this.transformation.MoveY = moveYy;
                    this.transformation.ScaleX = 1;
                    this.transformation.ScaleY = scaleYx * scaleYy;
                    this._face.LeftEye.Transformation.setDestin(this.transformation);

                    this._face.RightEye.Transformation.Animation.restart(t);
                    this._face.LeftEye.Transformation.Animation.restart(t);
                }

                update(t) {
                    this.Timer.update(t);
                    if (this.Timer.isExpired()) {
                        this.Timer.reset(t);
                        const x = Math.floor(Math.random() * 100) - 50;
                        const y = Math.floor(Math.random() * 100) - 50;
                        this.lookAt(x / 100, y / 100, t);
                    }
                }
            }

            class FaceExpression {
                constructor(face) {
                    this._face = face;
                }

                sv(v) { return v * GEOMETRY_SCALE; }

                clearVariations(t = nowMs()) {
                    this._face.RightEye.Variation1.clear();
                    this._face.RightEye.Variation2.clear();
                    this._face.LeftEye.Variation1.clear();
                    this._face.LeftEye.Variation2.clear();
                    this._face.RightEye.Variation1.Animation.restart(t);
                    this._face.LeftEye.Variation1.Animation.restart(t);
                }

                goToNormal(t = nowMs()) {
                    this.clearVariations(t);
                    this._face.RightEye.Variation1.Values.Height = this.sv(3);
                    this._face.RightEye.Variation2.Values.Width = this.sv(1);
                    this._face.LeftEye.Variation1.Values.Height = this.sv(2);
                    this._face.LeftEye.Variation2.Values.Width = this.sv(2);
                    this._face.RightEye.Variation1.Animation.setTriangle(1000, 0);
                    this._face.LeftEye.Variation1.Animation.setTriangle(1000, 0);
                    this._face.RightEye.transitionTo(PRESETS.Normal, t);
                    this._face.LeftEye.transitionTo(PRESETS.Normal, t);
                }

                goToAngry(t = nowMs()) {
                    this.clearVariations(t);
                    this._face.RightEye.Variation1.Values.OffsetY = this.sv(2);
                    this._face.LeftEye.Variation1.Values.OffsetY = this.sv(2);
                    this._face.RightEye.Variation1.Animation.setTriangle(300, 0);
                    this._face.LeftEye.Variation1.Animation.setTriangle(300, 0);
                    this._face.RightEye.transitionTo(PRESETS.Angry, t);
                    this._face.LeftEye.transitionTo(PRESETS.Angry, t);
                }

                goToGlee(t = nowMs()) {
                    this.clearVariations(t);
                    this._face.RightEye.Variation1.Values.OffsetY = this.sv(5);
                    this._face.LeftEye.Variation1.Values.OffsetY = this.sv(5);
                    this._face.RightEye.Variation1.Animation.setTriangle(300, 0);
                    this._face.LeftEye.Variation1.Animation.setTriangle(300, 0);
                    this._face.RightEye.transitionTo(PRESETS.Glee, t);
                    this._face.LeftEye.transitionTo(PRESETS.Glee, t);
                }

                goToHappy(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Happy, t); this._face.LeftEye.transitionTo(PRESETS.Happy, t); }
                goToSad(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Sad, t); this._face.LeftEye.transitionTo(PRESETS.Sad, t); }
                goToWorried(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Worried, t); this._face.LeftEye.transitionTo(PRESETS.Worried_Alt, t); }
                goToFocused(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Focused, t); this._face.LeftEye.transitionTo(PRESETS.Focused, t); }
                goToAnnoyed(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Annoyed, t); this._face.LeftEye.transitionTo(PRESETS.Annoyed_Alt, t); }
                goToSurprised(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Surprised, t); this._face.LeftEye.transitionTo(PRESETS.Surprised, t); }
                goToSkeptic(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Skeptic, t); this._face.LeftEye.transitionTo(PRESETS.Skeptic_Alt, t); }
                goToFrustrated(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Frustrated, t); this._face.LeftEye.transitionTo(PRESETS.Frustrated, t); }
                goToUnimpressed(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Unimpressed, t); this._face.LeftEye.transitionTo(PRESETS.Unimpressed_Alt, t); }
                goToSleepy(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Sleepy, t); this._face.LeftEye.transitionTo(PRESETS.Sleepy_Alt, t); }
                goToSuspicious(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Suspicious, t); this._face.LeftEye.transitionTo(PRESETS.Suspicious_Alt, t); }

                goToSquint(t = nowMs()) {
                    this.clearVariations(t);
                    this._face.LeftEye.Variation1.Values.OffsetX = this.sv(6);
                    this._face.LeftEye.Variation2.Values.OffsetY = this.sv(6);
                    this._face.RightEye.transitionTo(PRESETS.Squint, t);
                    this._face.LeftEye.transitionTo(PRESETS.Squint_Alt, t);
                }

                goToFurious(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Furious, t); this._face.LeftEye.transitionTo(PRESETS.Furious, t); }
                goToScared(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Scared, t); this._face.LeftEye.transitionTo(PRESETS.Scared, t); }
                goToAwe(t = nowMs()) { this.clearVariations(t); this._face.RightEye.transitionTo(PRESETS.Awe, t); this._face.LeftEye.transitionTo(PRESETS.Awe, t); }
            }

            class FaceBehavior {
                constructor(face) {
                    this._face = face;
                    this.CurrentEmotion = 0;
                }

                goToEmotion(index, t = nowMs()) {
                    this.CurrentEmotion = index;
                    switch (index) {
                        case 0: this._face.Expression.goToNormal(t); break;
                        case 1: this._face.Expression.goToAngry(t); break;
                        case 2: this._face.Expression.goToGlee(t); break;
                        case 3: this._face.Expression.goToHappy(t); break;
                        case 4: this._face.Expression.goToSad(t); break;
                        case 5: this._face.Expression.goToWorried(t); break;
                        case 6: this._face.Expression.goToFocused(t); break;
                        case 7: this._face.Expression.goToAnnoyed(t); break;
                        case 8: this._face.Expression.goToSurprised(t); break;
                        case 9: this._face.Expression.goToSkeptic(t); break;
                        case 10: this._face.Expression.goToFrustrated(t); break;
                        case 11: this._face.Expression.goToUnimpressed(t); break;
                        case 12: this._face.Expression.goToSleepy(t); break;
                        case 13: this._face.Expression.goToSuspicious(t); break;
                        case 14: this._face.Expression.goToSquint(t); break;
                        case 15: this._face.Expression.goToFurious(t); break;
                        case 16: this._face.Expression.goToScared(t); break;
                        case 17: this._face.Expression.goToAwe(t); break;
                        default: break;
                    }
                }
            }

            class Face {
                constructor() {
                    this.EyeSize = 40 * GEOMETRY_SCALE;
                    this.EyeInterDistance = 4 * GEOMETRY_SCALE;
                    this.CenterX = STAGE_W / 2;
                    this.CenterY = STAGE_H / 2;
                    this.LeftEye = new Eye(this);
                    this.RightEye = new Eye(this);
                    this.Blink = new BlinkAssistant(this);
                    this.Look = new LookAssistant(this);
                    this.Behavior = new FaceBehavior(this);
                    this.Expression = new FaceExpression(this);
                    this.LeftEye.IsMirrored = true;
                    this.RandomLook = true;
                    this.RandomBlink = true;
                }

                doBlink(t = nowMs()) { this.Blink.blink(t); }

                lookFront(t = nowMs()) { this.Look.lookAt(0, 0, t); }

                update(t = nowMs()) {
                    if (this.RandomLook) this.Look.update(t);
                    if (this.RandomBlink) this.Blink.update(t);
                    this.LeftEye.update(t);
                    this.RightEye.update(t);
                }

                getLeftCenter() {
                    return { x: this.CenterX - this.EyeSize / 2 - this.EyeInterDistance, y: this.CenterY };
                }

                getRightCenter() {
                    return { x: this.CenterX + this.EyeSize / 2 + this.EyeInterDistance, y: this.CenterY };
                }
            }

            function fmt(v) { return Number(v).toFixed(2); }

            function normalize(x, y) {
                const len = Math.hypot(x, y);
                if (len < 1e-6) return { x: 0, y: 0, len: 0 };
                return { x: x / len, y: y / len, len };
            }

            function roundPolygonPath(points, radii) {
                const n = points.length;
                const ins = new Array(n);
                const outs = new Array(n);

                for (let i = 0; i < n; i++) {
                    const prev = points[(i - 1 + n) % n];
                    const curr = points[i];
                    const next = points[(i + 1) % n];
                    const inVecRaw = { x: prev.x - curr.x, y: prev.y - curr.y };
                    const outVecRaw = { x: next.x - curr.x, y: next.y - curr.y };
                    const inVec = normalize(inVecRaw.x, inVecRaw.y);
                    const outVec = normalize(outVecRaw.x, outVecRaw.y);
                    const r = Math.max(0, radii[i] || 0);
                    const d = Math.min(r, inVec.len * 0.5, outVec.len * 0.5);
                    ins[i] = { x: curr.x + inVec.x * d, y: curr.y + inVec.y * d };
                    outs[i] = { x: curr.x + outVec.x * d, y: curr.y + outVec.y * d };
                }

                let d = `M ${fmt(ins[0].x)} ${fmt(ins[0].y)}`;
                for (let i = 0; i < n; i++) {
                    const curr = points[i];
                    const outP = outs[i];
                    const nextIn = ins[(i + 1) % n];
                    d += ` Q ${fmt(curr.x)} ${fmt(curr.y)} ${fmt(outP.x)} ${fmt(outP.y)}`;
                    d += ` L ${fmt(nextIn.x)} ${fmt(nextIn.y)}`;
                }
                d += ' Z';
                return d;
            }

            function eyePathFromConfig(centerX, centerY, cfgRaw) {
                const cfg = cloneConfig(cfgRaw);
                cfg.Width = Math.max(2, cfg.Width);
                cfg.Height = Math.max(2, cfg.Height);
                cfg.Radius_Top = Math.max(0, cfg.Radius_Top);
                cfg.Radius_Bottom = Math.max(0, cfg.Radius_Bottom);

                const deltaTop = cfg.Height * cfg.Slope_Top * 0.5;
                const deltaBottom = cfg.Height * cfg.Slope_Bottom * 0.5;
                const totalHeight = cfg.Height + deltaTop - deltaBottom;

                if (cfg.Radius_Bottom > 0 && cfg.Radius_Top > 0 && totalHeight - 1 < cfg.Radius_Bottom + cfg.Radius_Top) {
                    const ratio = (totalHeight - 1) / (cfg.Radius_Bottom + cfg.Radius_Top);
                    cfg.Radius_Top *= ratio;
                    cfg.Radius_Bottom *= ratio;
                }

                const xLeft = centerX + cfg.OffsetX - cfg.Width / 2;
                const xRight = centerX + cfg.OffsetX + cfg.Width / 2;
                const yTopBase = centerY + cfg.OffsetY - cfg.Height / 2;
                const yBottomBase = centerY + cfg.OffsetY + cfg.Height / 2;

                const points = [
                    { x: xLeft, y: yTopBase - deltaTop },
                    { x: xRight, y: yTopBase + deltaTop },
                    { x: xRight, y: yBottomBase + deltaBottom },
                    { x: xLeft, y: yBottomBase - deltaBottom }
                ];

                const radii = [cfg.Radius_Top, cfg.Radius_Top, cfg.Radius_Bottom, cfg.Radius_Bottom];
                return roundPolygonPath(points, radii);
            }

            const leftEyePath = document.getElementById('faceLeftEye');
            const rightEyePath = document.getElementById('faceRightEye');

            if (!leftEyePath || !rightEyePath) return;

            const face = new Face();
            face.Behavior.goToEmotion(0);

            let lastFrame = 0;
            function animate(ts) {
                if (!lastFrame || ts - lastFrame >= EYES_FRAME_MS) {
                    lastFrame = ts;
                    face.update(ts);
                    const leftCenter = face.getLeftCenter();
                    const rightCenter = face.getRightCenter();
                    leftEyePath.setAttribute('d', eyePathFromConfig(leftCenter.x, leftCenter.y, face.LeftEye.FinalConfig));
                    rightEyePath.setAttribute('d', eyePathFromConfig(rightCenter.x, rightCenter.y, face.RightEye.FinalConfig));
                }
                requestAnimationFrame(animate);
            }

            requestAnimationFrame(animate);

            window.robotFace = {
                emotions: EMOTIONS.slice(),
                setEmotion: function (emotion) {
                    let idx = -1;
                    if (typeof emotion === 'number') {
                        idx = Math.max(0, Math.min(EMOTIONS.length - 1, Math.round(emotion)));
                    } else if (typeof emotion === 'string') {
                        const target = emotion.trim().toLowerCase();
                        idx = EMOTIONS.findIndex(function (name) { return name.toLowerCase() === target; });
                    }
                    if (idx >= 0) face.Behavior.goToEmotion(idx);
                },
                blink: function () {
                    face.doBlink();
                },
                lookCenter: function () {
                    face.lookFront();
                },
                setRandomLook: function (enabled) {
                    face.RandomLook = Boolean(enabled);
                },
                setRandomBlink: function (enabled) {
                    face.RandomBlink = Boolean(enabled);
                }
            };
        })();
    }

    global.WebRobotBody = global.WebRobotBody || {};
    global.WebRobotBody.initRobotFacePanel = initRobotFacePanel;
})(window);
